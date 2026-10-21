package linkedform

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
	"github.com/kimposoft/kimpo-plugin-sdk/plugin"
)

// linkedFormHostContext 起一个空 bufconn gRPC server，构造真实 SDK HostClient。V1
// ActionQueryService 已从 wire 和 manifest 同时删除；这些用例只需要证明非空宿主句柄下，
// 插件按 query_context 中的宿主 V4 派发结果执行或稳定 fail-closed。
func linkedFormHostContext(t *testing.T) context.Context {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return plugin.WithHost(context.Background(), plugin.NewHostClient(conn))
}

func linkedFormExecuteRequest(t *testing.T) *bizpb.ExecuteRequest {
	t.Helper()
	ctx, err := structpb.NewStruct(map[string]any{
		"params": map[string]any{
			"link_mode":   "open",
			"template_id": "tpl-target",
			"open_mode":   "edit",
			// linked_form_v4：替换旧版 query 键（硬切换）。
			"linked_form_v4": map[string]any{
				"query": map[string]any{
					"source": map[string]any{
						"tables": []any{map[string]any{"alias": "target", "template_id": "tpl-target"}},
					},
				},
				"target":  map[string]any{"template_id": "tpl-target", "data_table_id": "dt-target"},
				"preview": []any{},
				"prefill": []any{},
			},
		},
		"query_context": map[string]any{
			"query_grant": "query-grant-1",
			"action_id":   "node-linked-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &bizpb.ExecuteRequest{
		ActionName:     ActionName,
		Context:        ctx,
		TriggerContext: &bizpb.TriggerContextMsg{Locale: "en-US"},
	}
}

// TestExecuteFailsClosedWithoutHostOrHostResult 覆盖两种 fail-closed 情形：
//   - 完全没有宿主句柄（host==nil）→ LINKED_FORM_NO_HOST；
//   - 宿主句柄存在但本次派发未命中 V4 派发结果（query_context 缺
//     alice_linked_form_result）→ V1 查询通道已移除：LINKED_FORM_V1_UNSUPPORTED_OPEN。
//     本用例原名 TestExecuteFailsClosedWithoutHostOrQueryPermission；V1 查询回落已删除，
//     第二种情形现在只验证宿主回执缺席时的稳定失败。
func TestExecuteFailsClosedWithoutHostOrHostResult(t *testing.T) {
	req := linkedFormExecuteRequest(t)

	resp, err := New().Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_NO_HOST" ||
		resp.GetMessageKey() != "linked_form.no_host" || resp.GetResult() != nil {
		t.Fatalf("missing-host response=%+v", resp)
	}

	resp, err = New().Execute(linkedFormHostContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_V1_UNSUPPORTED_OPEN" ||
		resp.GetMessageKey() != "linked_form.v1_unsupported_open" || resp.GetResult() != nil {
		t.Fatalf("no-host-result response=%+v", resp)
	}
}

// TestExecuteRealHostFailsClosedWithoutHostResult 用真实 SDK HostClient/bufconn 验证：
// 宿主句柄存在但 query_context 不含 V4 派发结果时，插件稳定 fail-closed。
func TestExecuteRealHostFailsClosedWithoutHostResult(t *testing.T) {
	resp, err := New().Execute(linkedFormHostContext(t), linkedFormExecuteRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_V1_UNSUPPORTED_OPEN" ||
		resp.GetMessageKey() != "linked_form.v1_unsupported_open" || resp.GetResult() != nil {
		t.Fatalf("response=%+v", resp)
	}
}

// linkedFormExecuteRequestWithQueryContext 复用 linkedFormExecuteRequest 的
// params 形状，只替换 query_context——用于验证命中 alice_linked_form_result 键时
// 插件消费回执、以及键缺席时 fail-closed 两类场景。
func linkedFormExecuteRequestWithQueryContext(t *testing.T, linkMode string, queryContext map[string]any) *bizpb.ExecuteRequest {
	t.Helper()
	params := map[string]any{
		"link_mode":   linkMode,
		"template_id": "tpl-target",
		"open_mode":   "edit",
		"linked_form_v4": map[string]any{
			"query": map[string]any{
				"source": map[string]any{
					"tables": []any{map[string]any{"alias": "target", "template_id": "tpl-target"}},
				},
			},
			"target":  map[string]any{"template_id": "tpl-target", "data_table_id": "dt-target"},
			"preview": []any{},
			"prefill": []any{},
		},
	}
	ctx, err := structpb.NewStruct(map[string]any{
		"params":        params,
		"query_context": queryContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &bizpb.ExecuteRequest{
		ActionName:     ActionName,
		Context:        ctx,
		TriggerContext: &bizpb.TriggerContextMsg{Locale: "en-US"},
	}
}

// TestExecuteOpenModeShortCircuitsOnHostResult 覆盖宿主结果短路的核心
// 判据：query_context 携带 alice_linked_form_result（mode=open，1 行命中）时，
// 插件必须直接用宿主结果组装 form.open 效果。
func TestExecuteOpenModeShortCircuitsOnHostResult(t *testing.T) {
	req := linkedFormExecuteRequestWithQueryContext(t, "open", map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode": "open",
			"rows": []any{
				map[string]any{"record_id": "record-host-1", "preview": map[string]any{"field.target-name": "alice"}},
			},
			"row_count":   1,
			"plan_digest": "sha256:linked-form-host",
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	if len(effects) != 1 {
		t.Fatalf("effects=%v", effects)
	}
	effect, _ := effects[0].(map[string]any)
	args, _ := effect["args"].(map[string]any)
	if effect["command"] != "form.open" || args["record_id"] != "record-host-1" || args["target_template_id"] != "tpl-target" {
		t.Fatalf("effect=%+v", effect)
	}
}

// TestExecuteOpenModeMultiHitFallsBackToHostPreviewKeys 覆盖预览列兜底
// 判据：设计器未配置预览列（params 无 preview 键，normalizePreview 兜底空）、且
// 宿主 N 命中（mode=open）时，插件必须改用宿主结果行自带的 Preview 键（宿主执行
// 期编译层自动补的「默认预览列」）组装 form.pick-open 的 preview_field_ids 与
// 每条 record 的 preview，而不是像修前那样把预览整体丢空。
func TestExecuteOpenModeMultiHitFallsBackToHostPreviewKeys(t *testing.T) {
	req := linkedFormExecuteRequestWithQueryContext(t, "open", map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode": "open",
			"rows": []any{
				map[string]any{"record_id": "record-host-1", "preview": map[string]any{"field.target-name": "甲"}},
				map[string]any{"record_id": "record-host-2", "preview": map[string]any{"field.target-name": "乙"}},
			},
			"row_count":   2,
			"plan_digest": "sha256:linked-form-host-multi",
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	if len(effects) != 1 {
		t.Fatalf("effects=%v", effects)
	}
	effect, _ := effects[0].(map[string]any)
	if effect["command"] != "form.pick-open" {
		t.Fatalf("command = %v, want form.pick-open", effect["command"])
	}
	args, _ := effect["args"].(map[string]any)
	ids, _ := args["preview_field_ids"].([]any)
	if len(ids) != 1 || ids[0] != "field.target-name" {
		t.Fatalf("preview_field_ids = %v, want [field.target-name]（宿主默认预览列兜底）", ids)
	}
	records, _ := args["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("records=%v", records)
	}
	r0, _ := records[0].(map[string]any)
	preview0, _ := r0["preview"].(map[string]any)
	if r0["record_id"] != "record-host-1" || preview0["field.target-name"] != "甲" {
		t.Fatalf("records[0]=%+v", r0)
	}
	r1, _ := records[1].(map[string]any)
	preview1, _ := r1["preview"].(map[string]any)
	if r1["record_id"] != "record-host-2" || preview1["field.target-name"] != "乙" {
		t.Fatalf("records[1]=%+v", r1)
	}
}

// TestExecuteOpenModeFailsClosedWithoutHostResultKey 覆盖 V1 查询通道移除后的
// 判据：query_context 不携带 alice_linked_form_result 键（未接线/引用了
// bt 明细字段等暂不支持的形状）时，插件不再回落调用 SubmitExpression，直接报
// 具名错误 LINKED_FORM_V1_UNSUPPORTED_OPEN。本用例原名
// TestExecuteOpenModeFallsBackWithoutHostResultKey、原本验证"逐字节回落今天的行为
// ——照常调 SubmitExpression"；该行为已随本批删除，断言整体反转。
func TestExecuteOpenModeFailsClosedWithoutHostResultKey(t *testing.T) {
	req := linkedFormExecuteRequestWithQueryContext(t, "open", map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_V1_UNSUPPORTED_OPEN" ||
		resp.GetMessageKey() != "linked_form.v1_unsupported_open" || resp.GetResult() != nil {
		t.Fatalf("response=%+v", resp)
	}
}

// TestExecuteCreateModeFailsClosedWithoutHostResultKey 覆盖 create 模式对应的
// fail-closed 判据：填充映射非空（需要表达式求值）、且 query_context 未携带
// alice_linked_form_result 键时，V1 查询通道移除后不再回落
// host.Query().SubmitExpression 求值——直接报 LINKED_FORM_V1_UNSUPPORTED_CREATE，
// SubmitExpression 绝不会被调用。
func TestExecuteCreateModeFailsClosedWithoutHostResultKey(t *testing.T) {
	ctx, err := structpb.NewStruct(map[string]any{
		"params": map[string]any{
			"link_mode":   "create",
			"template_id": "tpl-target",
			"linked_form_v4": map[string]any{
				"target": map[string]any{"template_id": "tpl-target", "data_table_id": "dt-target"},
				// 填充映射非空（引用本表单主表字段 bt0），需要表达式求值——V4 起该映射
				// 由宿主保存闸在设计期校验合法性，本插件不再读表达式内容做静态检查，
				// 只按"非空即需要宿主回执"判据分流。
				"prefill": []any{
					map[string]any{"target_field_id": "fx", "expression": map[string]any{"kind": "source_field", "alias": "bt0", "field_id": "f1"}},
				},
			},
		},
		"query_context": map[string]any{
			"query_grant": "query-grant-1",
			"action_id":   "node-linked-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := &bizpb.ExecuteRequest{
		ActionName:     ActionName,
		Context:        ctx,
		TriggerContext: &bizpb.TriggerContextMsg{Locale: "en-US"},
	}

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_V1_UNSUPPORTED_CREATE" ||
		resp.GetMessageKey() != "linked_form.v1_unsupported_create" || resp.GetResult() != nil {
		t.Fatalf("response=%+v", resp)
	}
}

// TestExecuteCreateModeZeroOutputsStillOpensWithoutHostResult 验证 V1 查询通道移除
// 只删除了"填充映射非空、需要引擎求值"这一条 V1 回落分支——映射本就为空（无需
// 任何引擎求值）时不受影响，无论是否命中宿主结果都应继续直接打开零预填的填报弹窗。
func TestExecuteCreateModeZeroOutputsStillOpensWithoutHostResult(t *testing.T) {
	req := linkedFormExecuteRequestWithQueryContext(t, "create", map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	effect, _ := effects[0].(map[string]any)
	args, _ := effect["args"].(map[string]any)
	prefill, _ := args["prefill"].([]any)
	if effect["command"] != "form.open-create" || len(prefill) != 0 {
		t.Fatalf("effect=%+v", effect)
	}
}

// TestExecuteCreateModeShortCircuitsOnHostResult 覆盖 create 模式的同一判据：
// 命中 alice_linked_form_result（mode=create）时直接用 Prefill 组装
// form.open-create，不调 SubmitExpression。
func TestExecuteCreateModeShortCircuitsOnHostResult(t *testing.T) {
	req := linkedFormExecuteRequestWithQueryContext(t, "create", map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode":    "create",
			"prefill": map[string]any{"field.target-name": "prefilled-by-host"},
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	effect, _ := effects[0].(map[string]any)
	args, _ := effect["args"].(map[string]any)
	if effect["command"] != "form.open-create" {
		t.Fatalf("effect=%+v", effect)
	}
	prefill, _ := args["prefill"].([]any)
	if len(prefill) != 1 {
		t.Fatalf("prefill=%v", prefill)
	}
	item, _ := prefill[0].(map[string]any)
	if item["field_id"] != "field.target-name" || item["text"] != "prefilled-by-host" {
		t.Fatalf("unexpected prefill item: %+v", item)
	}
}

// ── open 零命中 + create_when_missing 的三分流 ──────────────────────────────

// linkedFormOpenZeroHitRequest 构造 link_mode=open + create_when_missing=<给定值> 的
// Execute 请求，linked_form_v4.prefill 用 prefillOutputs 显式给定（用于区分"设计态本就
// 配置了填充映射"与"零映射"两类场景），query_context 用 queryContext 显式给定（用于
// 覆盖宿主回执带/不带 prefill 键两类场景）。
func linkedFormOpenZeroHitRequest(t *testing.T, createWhenMissing bool, prefillOutputs []any, queryContext map[string]any) *bizpb.ExecuteRequest {
	t.Helper()
	ctx, err := structpb.NewStruct(map[string]any{
		"params": map[string]any{
			"link_mode":           "open",
			"template_id":         "tpl-target",
			"open_mode":           "edit",
			"create_when_missing": createWhenMissing,
			"linked_form_v4": map[string]any{
				"query": map[string]any{
					"source": map[string]any{
						"tables": []any{map[string]any{"alias": "target", "template_id": "tpl-target"}},
					},
				},
				"target":  map[string]any{"template_id": "tpl-target", "data_table_id": "dt-target"},
				"preview": []any{},
				"prefill": prefillOutputs,
			},
		},
		"query_context": queryContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &bizpb.ExecuteRequest{
		ActionName:     ActionName,
		Context:        ctx,
		TriggerContext: &bizpb.TriggerContextMsg{Locale: "en-US"},
	}
}

// TestExecuteOpenZeroHitCreateWhenMissingUsesHostPrefill 覆盖三分流的主判据：open
// 零命中，宿主在同一次派发的回执里额外带上非空 prefill（mode 仍为 open，rows=[]），
// 插件必须直接用它组装 form.open-create（与 create 分派同款 buildFillEffectFromHostPrefill），
// 不落回 buildFillEffect 兜底路径。
func TestExecuteOpenZeroHitCreateWhenMissingUsesHostPrefill(t *testing.T) {
	req := linkedFormOpenZeroHitRequest(t, true, []any{}, map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode":      "open",
			"rows":      []any{},
			"row_count": 0,
			"prefill":   map[string]any{"field.target-name": "prefilled-on-miss"},
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	if len(effects) != 1 {
		t.Fatalf("effects=%v", effects)
	}
	effect, _ := effects[0].(map[string]any)
	if effect["command"] != "form.open-create" {
		t.Fatalf("effect=%+v", effect)
	}
	args, _ := effect["args"].(map[string]any)
	prefill, _ := args["prefill"].([]any)
	if len(prefill) != 1 {
		t.Fatalf("prefill=%v", prefill)
	}
	item, _ := prefill[0].(map[string]any)
	if item["field_id"] != "field.target-name" || item["text"] != "prefilled-on-miss" {
		t.Fatalf("unexpected prefill item: %+v", item)
	}
}

// TestExecuteOpenZeroHitCreateWhenMissingUsesHostPrefillEmptyMap 覆盖三分流的空
// map 边界：prefill 键出现但值是空 map——表示宿主已经算过（不是"没算"），零预填是
// 合法结果，插件仍应走 buildFillEffectFromHostPrefill（而不是误判"键缺席"落回兜底）。
func TestExecuteOpenZeroHitCreateWhenMissingUsesHostPrefillEmptyMap(t *testing.T) {
	req := linkedFormOpenZeroHitRequest(t, true, []any{}, map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode":      "open",
			"rows":      []any{},
			"row_count": 0,
			"prefill":   map[string]any{}, // 键出现、值为空 map：宿主已算过，零预填。
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	effect, _ := effects[0].(map[string]any)
	if effect["command"] != "form.open-create" {
		t.Fatalf("effect=%+v", effect)
	}
	args, _ := effect["args"].(map[string]any)
	prefill, _ := args["prefill"].([]any)
	if len(prefill) != 0 {
		t.Fatalf("prefill=%v, want empty", prefill)
	}
}

// TestExecuteOpenZeroHitCreateWhenMissingWithoutHostPrefillFallsBackToLegacy 覆盖
// "键缺席才走原兜底"：宿主回执（mode=open）不携带 prefill 键（理论上不该发生，但
// 保持防御），且设计态 linked_form_v4.prefill 非空（需要表达式求值）——必须落回
// buildFillEffect 兜底路径，报与直接 create 分派一致的 LINKED_FORM_V1_UNSUPPORTED_CREATE，
// 而不是误把空态当成"host 已计算零预填"直接放行。
func TestExecuteOpenZeroHitCreateWhenMissingWithoutHostPrefillFallsBackToLegacy(t *testing.T) {
	req := linkedFormOpenZeroHitRequest(t, true, []any{
		map[string]any{"target_field_id": "fx", "expression": map[string]any{"kind": "source_field", "alias": "bt0", "field_id": "f1"}},
	}, map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode":      "open",
			"rows":      []any{},
			"row_count": 0,
			// prefill 键缺席。
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_V1_UNSUPPORTED_CREATE" ||
		resp.GetMessageKey() != "linked_form.v1_unsupported_create" || resp.GetResult() != nil {
		t.Fatalf("response=%+v", resp)
	}
}

// TestExecuteOpenZeroHitCreateWhenMissingWithoutHostPrefillZeroOutputsOpensBlank 是
// 上一条用例的镜像：prefill 键同样缺席，但设计态填充映射本就为空——维持修前既有行为，
// 直接打开零预填的填报弹窗，不报错。
func TestExecuteOpenZeroHitCreateWhenMissingWithoutHostPrefillZeroOutputsOpensBlank(t *testing.T) {
	req := linkedFormOpenZeroHitRequest(t, true, []any{}, map[string]any{
		"query_grant": "query-grant-1",
		"action_id":   "node-linked-1",
		"alice_linked_form_result": map[string]any{
			"mode":      "open",
			"rows":      []any{},
			"row_count": 0,
		},
	})

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	effects, _ := resp.GetResult().AsMap()["effects"].([]any)
	effect, _ := effects[0].(map[string]any)
	args, _ := effect["args"].(map[string]any)
	prefill, _ := args["prefill"].([]any)
	if effect["command"] != "form.open-create" || len(prefill) != 0 {
		t.Fatalf("effect=%+v", effect)
	}
}

// TestExecuteOpenModeMissingLinkedFormV4ReturnsNoQuery 覆盖 schema 硬切换后的判据：
// open 模式下 params 缺 linked_form_v4（新键）时报 LINKED_FORM_NO_QUERY（错误码不变，
// 只是触发条件从旧版 queryMap==nil 换成新版 linked_form_v4 是否为对象）。
func TestExecuteOpenModeMissingLinkedFormV4ReturnsNoQuery(t *testing.T) {
	ctx, err := structpb.NewStruct(map[string]any{
		"params": map[string]any{
			"link_mode":   "open",
			"template_id": "tpl-target",
			"open_mode":   "edit",
			// linked_form_v4 缺席。
		},
		"query_context": map[string]any{
			"query_grant": "query-grant-1",
			"action_id":   "node-linked-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := &bizpb.ExecuteRequest{
		ActionName:     ActionName,
		Context:        ctx,
		TriggerContext: &bizpb.TriggerContextMsg{Locale: "en-US"},
	}

	resp, err := New().Execute(linkedFormHostContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "LINKED_FORM_NO_QUERY" ||
		resp.GetMessageKey() != "linked_form.no_query" || resp.GetResult() != nil {
		t.Fatalf("response=%+v", resp)
	}
}
