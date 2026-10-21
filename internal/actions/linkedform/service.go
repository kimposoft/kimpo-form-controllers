// Package linkedform 实现 ui.linked_form 动作：联动表单（原 ui.open_form「打开表单」与
// ui.fill_form「填报表单」两动作的合并形态）。
//
// 语义：
//   - 先选目标模板（后续字段与条件均来自该模板）；
//   - 联动方式 link_mode=create：创建一张目标模板的新表单，并按表达式求值填充字段（弹窗填报）；
//   - 联动方式 link_mode=open：按打开条件（整段 WHERE 表达式）查找目标模板已有表单——
//     1 条命中 → 以查看/编辑模式（open_mode）打开；N 条命中 → 弹记录选择列表；
//     0 条命中 → 无效果；开启 create_when_missing（找不到时新建表单）时转走新建填充链。
//     （本动作不是 hit/miss 出口分支节点，后续节点走普通续接带恒续走。）
//
// 填充求值：节点参数 linked_form_v4（V4 文档对象，含 query/target/preview/prefill，
// 已替换旧版 V3.4 表达式模型键 query，硬切换）里 prefill（目标字段 ←
// 表达式）由宿主 V4 派发器（宿主侧文档展开 + V4 编译/执行链路）在派发前算好，随 query_context 回执给插件（见 SDK
// plugin.ParseLinkedFormResult，回执形状未变）；插件只消费回执，不再自己发起查询求值。
//
// V1 查询通道已完整移除：本包原有「宿主未命中时回落自建查询模型经
// host.Query().SubmitExpression 打 V1 查询通道」的两条分支（open 查询 / 新建填充求值）
// 已删除——硬切换，接不住的配置形状（如引用了本表单明细表字段）直接报
// 具名错误 LINKED_FORM_V1_UNSUPPORTED_OPEN / LINKED_FORM_V1_UNSUPPORTED_CREATE，
// 不再静默回落旧引擎（错误码沿用不改名）。
package linkedform

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kimposoft/kimpo-plugin-sdk/direction/biz"
	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
	"github.com/kimposoft/kimpo-plugin-sdk/plugin"
)

// ActionName 是本子包贡献的唯一动作：联动表单。导出供 router 做动作名分派，
// 避免与 GetActions 里声明的动作名发生字符串漂移。
const ActionName = "ui.linked_form"

// defaultAutoCloseOnSave 是 auto_close_on_save 缺省值：未配置时默认保存后自动关闭填报弹窗。
const defaultAutoCloseOnSave = true

// Service 实现 biz.BusinessPlugin：贡献并执行 ui.linked_form 动作。
// 取数只读宿主随 query_context 回执的 V4 派发结果（plugin.ParseLinkedFormResult，
// 纯解码、不发起查询）；弹窗打开/填报/保存执行体都在前端
// （editor 命令路由 + 宿主前端的设计器容器 / 填报弹窗 / 记录选择弹窗），
// 本插件只产出 command 效果声明意图，不写任何业务数据。
type Service struct {
	bizpb.UnimplementedBusinessPluginServer
}

func New() *Service { return &Service{} }

// linkedFormParamsSchemaMap 是 ui.linked_form 参数的 JSON-Schema（**单一来源**）：GetActions 据此
// 向宿主声明、ValidateParams 据此做轻量 schema 前置校验。
func linkedFormParamsSchemaMap() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		// 注：本动作**不是**出口分支节点（已移除 hit/miss 双出口）——「找不到时新建」
		// 选项已覆盖 miss 语义，后续节点走普通续接带，无论命中与否/弹窗保存或取消均照常续走。
		// x-editor=linked-form：设计器据此挂载联动表单专用配置面板（步骤条+联动方式卡片+
		// 打开条件+填充映射），不走通用 schema 渲染。
		"x-editor": "linked-form",
		"properties": map[string]any{
			// link_mode：联动方式。create=新建表单（填报弹窗）；open=按条件打开已有表单。
			"link_mode": map[string]any{
				"type":    "string",
				"enum":    []any{"create", "open"},
				"default": "create",
			},
			// template_id：目标模板（步骤①选定；与 query.target.target_template_id 一致，冗余
			// 顶层一份使 create-only 场景不依赖模型解析即可定位目标）。
			"template_id": map[string]any{"type": "string"},
			// linked_form_v4：V4 文档对象（硬切换替换旧版 V3.4 表达式模型键
			// query；旧键不再支持）。形状与宿主侧 V4 文档展开层一致：
			//   query   = 打开语义的来源/关联/筛选（定位既有单据，open 模式用）；
			//   target  = 目标模板主表；
			//   preview = 打开命中多条时的预览列映射（与旧顶层 preview 参数同构，
			//             宿主 V4 以本字段为准）；
			//   prefill = 新建填充映射（目标字段 ← 表达式，create / 找不到时新建 用，
			//             对应旧版 query.target.outputs）。
			// 本插件不解析这份文档，只随 params 原样透传给宿主；查询与求值全部由宿主
			// V4 派发器完成，经 query_context.alice_linked_form_result 回执给插件。
			"linked_form_v4": map[string]any{"type": "object"},
			// open_mode：打开模式（open 模式命中后以查看还是编辑打开）。
			"open_mode": map[string]any{
				"type":    "string",
				"enum":    []any{"view", "edit"},
				"default": "view",
			},
			// create_when_missing：找不到要打开的表单时，转为新建表单。
			"create_when_missing": map[string]any{"type": "boolean", "default": false},
			// mode：打开呈现方式（沿用原 open_form 契约；tab=页签导航打开）。
			"mode": map[string]any{
				"type":    "string",
				"enum":    []any{"tab", "dialog"},
				"default": "tab",
			},
			// auto_close_on_save：新建填报弹窗保存后自动关闭（沿用原 fill_form 契约）。
			"auto_close_on_save": map[string]any{"type": "boolean", "default": defaultAutoCloseOnSave},
			// preview：N 命中选择弹窗的预览列（输出映射形态，与 query.target.outputs 同构）。
			// 由设计器面板按**目标模板主表字段的隐藏设置**自动生成（is_list_visible 为真的字段，
			// expression=source_field 指向目标表别名），非用户手配（预览列随字段
			// 隐藏设置走，不提供手选多选框）。
			"preview": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target_field_id": map[string]any{"type": "string"},
						"fill_strategy":   map[string]any{"type": "string"},
						"expression":      map[string]any{"type": "object"},
					},
					"required": []any{"target_field_id"},
				},
			},
			// writeback：保存后写回（沿用原 fill_form 契约与宿主写回端点白名单形状）。
			// source=新表单字段，target=当前表单字段；服务端按节点白名单权威反查
			// （宿主表单填报写回端点）。
			"writeback": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean", "default": false},
					"mappings": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_field_id":      map[string]any{"type": "string"},
								"target_field_id":      map[string]any{"type": "string"},
								"target_data_table_id": map[string]any{"type": "string"},
							},
							"required": []any{"source_field_id", "target_field_id"},
						},
					},
				},
			},
		},
		"required": []any{"template_id"},
	}
}

// GetActions 声明联动表单动作及其参数 schema（宿主 registry 据此做通用 schema 前置校验）。
func (s *Service) GetActions(_ context.Context, _ *bizpb.GetActionsRequest) (*bizpb.GetActionsResponse, error) {
	schema, err := structpb.NewStruct(linkedFormParamsSchemaMap())
	if err != nil {
		return nil, err
	}
	return &bizpb.GetActionsResponse{
		Actions: []*bizpb.ActionDefinition{
			{
				Name:         ActionName,
				Title:        "联动表单",
				Description:  "新建或打开目标模板的表单：新建按表达式填充字段；打开按条件查找（多条弹选择、找不到可转新建，查看/编辑模式可选）",
				ParamsSchema: schema,
				Style:        &bizpb.ActionStyle{Shape: "rounded", Color: "info"},
				Capabilities: &bizpb.ActionCapabilities{Asyncable: true, SideEffecting: false, ExpressionBased: true},
				// 标准动作面板排序权重 70（承接原填报表单的位置，发起流程之后的第一个表单动作）。
				SortWeight: 70,
				// 动作面板功能分类（设计器右栏页签聚合依据；标准动作另有宿主 canonical 兜底防旧二进制漂移）。
				Category: "表单",
			},
		},
	}, nil
}

// Execute 按 link_mode 分流：
//   - open：只认宿主 V4 派发器随 query_context 回执的 open 结果——按命中数分流
//     form.open / form.pick-open；0 命中时若 create_when_missing 则转新建填充链（宿主在同一次 open 派发的回执里额外带上 prefill 键时，插件直接用它
//     组装新建填充效果，不需要"同一次派发内先 open 后 create 两次改写"），否则无效果；
//     未回执（宿主未命中该次派发）→ 具名错误 LINKED_FORM_V1_UNSUPPORTED_OPEN；
//   - create：只认宿主回执的 create 结果（Prefill）→ form.open-create（弹窗填报，await 回执
//     续段）；未回执且填充映射非空 → 具名错误 LINKED_FORM_V1_UNSUPPORTED_CREATE
//     （原「回落 host.Query().SubmitExpression 打 V1 查询通道」分支
//     已删除，硬切换不做静默兼容）。
//
// 本动作无出口分支（hit/miss 已移除）：不产出 outcome，后续节点走普通续接带恒续走。
func (s *Service) Execute(ctx context.Context, req *bizpb.ExecuteRequest) (*bizpb.ExecuteResponse, error) {
	ctxMap := contextMap(req.GetContext())
	loc := req.GetTriggerContext().GetLocale()

	params, _ := ctxMap["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}

	templateID := strings.TrimSpace(stringParam(params, "template_id", ""))
	if templateID == "" {
		return failure(loc, "LINKED_FORM_NO_TEMPLATE", "linked_form.no_template", nil), nil
	}
	linkMode := stringParam(params, "link_mode", "create")
	openMode := stringParam(params, "open_mode", "view")
	mode := stringParam(params, "mode", "tab")
	createWhenMissing, _ := params["create_when_missing"].(bool)
	autoClose := defaultAutoCloseOnSave
	if v, ok := params["auto_close_on_save"].(bool); ok {
		autoClose = v
	}
	// linked_form_v4：V4 文档对象（替换旧版 query 键，硬切换，旧键不再支持）。
	linkedFormV4Map, _ := params["linked_form_v4"].(map[string]any)

	host := plugin.HostFrom(ctx)
	if host == nil {
		return failure(loc, "LINKED_FORM_NO_HOST", "linked_form.no_host", nil), nil
	}
	// query_context 原样回传（含 grant / 系统变量 / 本表单值，平台据此编译执行），照 extract 的透传写法；
	// 本插件只从中解出宿主 V4 派发器的回执（ParseLinkedFormResult），不再自己拿它去发起查询
	// （原用于喂 host.Query().SubmitExpression 的 triggerContextJSON 序列化已随
	// V1 回落分支一并删除）。
	qc, _ := ctxMap["query_context"].(map[string]any)
	// node_id：query_context.action_id = 引擎侧 input.Node.NodeID（buildExecuteContext 恒注入）。
	nodeID := actionIDFromQueryContext(ctxMap)

	var effects []map[string]any

	// 宿主 V4 派发器命中检查——命中即直接用宿主已经编译+真执行（open）/
	// 纯求值（create）出的结果组装既有 command 效果。
	// V1 查询通道已完整移除：键缺席（未接线/引用了 bt 明细字段等暂不
	// 支持的形状）不再回落自建查询模型打 host.Query().SubmitExpression——
	// 硬切换，直接报具名错误，见下方两处 LINKED_FORM_V1_UNSUPPORTED_*。
	hostResult, hostHit := plugin.ParseLinkedFormResult(qc)

	if linkMode == "open" {
		if linkedFormV4Map == nil {
			return failure(loc, "LINKED_FORM_NO_QUERY", "linked_form.no_query", nil), nil
		}
		_, previewFieldIDs := normalizePreview(params["preview"])

		if !hostHit || hostResult.Mode != "open" {
			// 宿主 V4 派发器没有为本次派发计算出 open 结果——本插件不再回落旧版
			// 自建查询模型 + host.Query().SubmitExpression（该 V1 查询通道已
			// 下线）。常见成因：打开条件引用了本表单明细表字段等改写层
			// 尚未覆盖的形状；这是当前的可接受终态，
			// 不做静默兼容，直接报具名错误交由用户调整配置。
			return failure(loc, "LINKED_FORM_V1_UNSUPPORTED_OPEN", "linked_form.v1_unsupported_open", map[string]any{
				"node_id":     nodeID,
				"template_id": templateID,
			}), nil
		}
		logf(ctx, "INFO", "联动表单(打开)：命中宿主改写结果: rows=%d", hostResult.RowCount)
		rows := hostOpenRowsToLegacyShape(hostResult.Rows)

		if len(rows) == 0 {
			// 0 命中：开启「找不到时新建」则转新建填充链，否则本动作无效果（后续节点照常续走）。
			// open 零命中 + create_when_missing=true 时，宿主
			// V4 派发器会在**同一次派发**的回执里额外带上 prefill 键（打开回执：
			// mode=open、rows=[]、prefill={...}；prefill 键出现即表示宿主已算过新建
			// 预填，空 map 表示零预填）——不再需要"同一次派发内先 open 后 create 两次
			// 改写"。hostResult.Prefill != nil 即命中该键，直接复用 create 分派同款
			// buildFillEffectFromHostPrefill 组装新建填充效果；键缺席（宿主没算，理论上
			// 不应发生但保持防御）才走原兜底 buildFillEffect（映射非空时会命中
			// LINKED_FORM_V1_UNSUPPORTED_CREATE，与直接 create 分派的落地行为一致）。
			if createWhenMissing {
				if hostResult.Prefill != nil {
					effects = []map[string]any{s.buildFillEffectFromHostPrefill(ctx, hostResult.Prefill, templateID, autoClose, nodeID, params["writeback"])}
				} else {
					fillEffect, ferr2 := s.buildFillEffect(ctx, loc, nodeID, linkedFormV4Map, templateID, autoClose, params["writeback"])
					if ferr2 != nil {
						return ferr2, nil
					}
					effects = []map[string]any{fillEffect}
				}
			} else {
				effects = []map[string]any{}
			}
		} else {
			// 设计器未配预览列（previewFieldIDs 为空）时，宿主
			// 执行期编译层会自动补一列「默认预览列」（目标主表第一个可用业务字段），
			// 命中行的 Preview 里带该列（键=target_field_id），但本插件此前只认
			// normalizePreview 从设计态 params.preview 生成的列，宿主自动补的这列被
			// 丢弃 ⇒ N 命中选择弹窗只剩「操作」列。这里补一条兜底：设计态未配置时，
			// 改用宿主结果行本身携带的预览键（稳定排序，跳过空键与 record_id）。
			if len(previewFieldIDs) == 0 {
				previewFieldIDs = fallbackPreviewFieldIDsFromHostRows(hostResult.Rows)
			}
			effects = buildOpenResult(rows, previewFieldIDs, mode, openMode, templateID)
		}
	} else {
		if hostHit && hostResult.Mode == "create" {
			logf(ctx, "INFO", "联动表单(新建)：命中宿主改写结果")
			effects = []map[string]any{s.buildFillEffectFromHostPrefill(ctx, hostResult.Prefill, templateID, autoClose, nodeID, params["writeback"])}
		} else {
			fillEffect, ferr := s.buildFillEffect(ctx, loc, nodeID, linkedFormV4Map, templateID, autoClose, params["writeback"])
			if ferr != nil {
				return ferr, nil
			}
			effects = []map[string]any{fillEffect}
		}
	}

	result, rerr := structpb.NewStruct(map[string]any{
		"effects": effectsToAny(effects),
	})
	if rerr != nil {
		return failure(loc, "LINKED_FORM_RESULT_ENCODE_FAILED", "linked_form.result_encode_failed", map[string]any{"detail": rerr}), nil
	}
	return &bizpb.ExecuteResponse{Success: true, Result: result}, nil
}

// ValidateParams 联动表单不做额外业务校验（query 的结构合法性由平台 expression.Validate + 编译期
// 兜底），恒通过——schema 层校验由宿主 registry 兜底，与 form-control 等插件一致。
func (s *Service) ValidateParams(_ context.Context, _ *bizpb.ValidateParamsRequest) (*bizpb.ValidateParamsResponse, error) {
	return &bizpb.ValidateParamsResponse{Valid: true}, nil
}

// ── 打开链（open）────────────────────────────────────────────────────────────

// normalizePreview 规整 params.preview（面板按目标主表字段隐藏设置生成的预览输出映射）：
// 丢弃缺 target_field_id / 撞 record_id 的非法项，返回可直接拼进查询 outputs 的列表 +
// 按声明序的预览字段 ID（= N 命中选择弹窗的列序）。缺失/非法整体安全兜底为空。
func normalizePreview(raw any) ([]any, []string) {
	items, _ := raw.([]any)
	outputs := make([]any, 0, len(items))
	fieldIDs := make([]string, 0, len(items))
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		fid, _ := m["target_field_id"].(string)
		fid = strings.TrimSpace(fid)
		if fid == "" || fid == "record_id" {
			continue
		}
		outputs = append(outputs, m)
		fieldIDs = append(fieldIDs, fid)
	}
	return outputs, fieldIDs
}

// buildOpenResult 是纯函数：按查询命中行数（≥1）分流。
//   - 1 行 → effects=[form.open]（打开单条）。
//   - N(≥2) 行 → effects=[form.pick-open]（宿主壳弹记录选择列表，预览列 =
//     previewFieldIDs——面板按目标主表字段隐藏设置自动生成，见 normalizePreview）。
//
// 0 命中的兜底（无效果 / create_when_missing 转新建）由调用方处理，不进本函数。
// rows 每行的 record_id 取自 hostOpenRowsToLegacyShape 折算的 "o_record_id" 列（沿用原 V1
// 查询结果的既有列名约定：平台编译器列别名 "o_"+target_field_id），预览值同理逐个取
// "o_<field_id>"；editType 即 open_mode（查看/编辑），targetTemplateID 透传供宿主 tab 导航。
func buildOpenResult(rows []map[string]any, previewFieldIDs []string, mode, editType, targetTemplateID string) []map[string]any {
	if len(rows) == 1 {
		effect := map[string]any{
			"kind":    "command",
			"command": "form.open",
			"args": map[string]any{
				"record_id":          recordIDFromRow(rows[0]),
				"mode":               mode,
				"edit_type":          editType,
				"target_template_id": targetTemplateID,
			},
		}
		return []map[string]any{effect}
	}

	records := make([]any, 0, len(rows))
	for _, row := range rows {
		preview := make(map[string]any, len(previewFieldIDs))
		for _, fid := range previewFieldIDs {
			preview[fid] = row["o_"+fid]
		}
		records = append(records, map[string]any{
			"record_id": recordIDFromRow(row),
			"preview":   preview,
		})
	}
	previewIDsAny := make([]any, 0, len(previewFieldIDs))
	for _, fid := range previewFieldIDs {
		previewIDsAny = append(previewIDsAny, fid)
	}
	effect := map[string]any{
		"kind":    "command",
		"command": "form.pick-open",
		"args": map[string]any{
			"records": records,
			// 预览列序显式下发（editor form.pick-open 优先读 args.preview_field_ids，
			// 缺失才按首条记录 preview 的 key 顺序兜底）。
			"preview_field_ids":  previewIDsAny,
			"mode":               mode,
			"edit_type":          editType,
			"target_template_id": targetTemplateID,
		},
		// E1 回执续段：多命中弹选择列表，用户选中/取消才算「语义完成」，需前端回执引擎恢复后续
		//（form.open 单条 tab 打开是即发即完，不标）。
		"await": true,
	}
	return []map[string]any{effect}
}

// hostOpenRowsToLegacyShape 把宿主改写层命中的行集（plugin.LinkedFormResultRow，
// RecordID + 已按 target_field_id 索引的 Preview map）折成 buildOpenResult 期望
// 的既有行形状（"o_record_id"/"o_<field_id>" 列，V1 SubmitExpression 结果行的
// 既有列名约定，见 recordIDFromRow/buildOpenResult 注释）——这样 buildOpenResult
// 不需要跟着改一行，宿主结果与 V1 查询结果对下游分流逻辑是同一形状。
func hostOpenRowsToLegacyShape(rows []plugin.LinkedFormResultRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{"o_record_id": row.RecordID}
		for fieldID, v := range row.Preview {
			m["o_"+fieldID] = v
		}
		out = append(out, m)
	}
	return out
}

// fallbackPreviewFieldIDsFromHostRows 是预览列兜底：设计器未配置
// 预览列时，改用宿主结果首行 Preview map 的键作为预览列（宿主执行期编译层此时会
// 自动补一列「默认预览列」=目标主表第一个可用业务字段，键即 target_field_id）。
// 跳过空键与 record_id（同 normalizePreview 的丢弃口径），按字典序稳定排序——
// Go map 遍历顺序不确定，多命中弹窗的列序不能随每次调用抖动。rows 为空时返回 nil。
func fallbackPreviewFieldIDsFromHostRows(rows []plugin.LinkedFormResultRow) []string {
	if len(rows) == 0 {
		return nil
	}
	fieldIDs := make([]string, 0, len(rows[0].Preview))
	for fid := range rows[0].Preview {
		fid = strings.TrimSpace(fid)
		if fid == "" || fid == "record_id" {
			continue
		}
		fieldIDs = append(fieldIDs, fid)
	}
	sort.Strings(fieldIDs)
	return fieldIDs
}

// buildFillEffectFromHostPrefill 用宿主改写层（create 模式）已经纯求值出的
// Prefill 组装 form.open-create 效果——与 buildFillEffect 的尾段（组装命令）逐字
// 同构，只是预填值来自宿主结果，本插件不再自己求值。
func (s *Service) buildFillEffectFromHostPrefill(
	ctx context.Context, hostPrefill map[string]any, templateID string, autoClose bool, nodeID string, writebackRaw any,
) map[string]any {
	prefill := prefillFromHostResult(hostPrefill)
	logf(ctx, "INFO", "联动表单(新建)：命中宿主改写结果，预填 %d 项，target_template_id=%s", len(prefill), templateID)
	return map[string]any{
		"kind":    "command",
		"command": "form.open-create",
		"args": map[string]any{
			"target_template_id": templateID,
			"prefill":            prefill,
			"auto_close_on_save": autoClose,
			"writeback":          normalizeWriteback(writebackRaw),
			"binding_id":         "",
			"node_id":            nodeID,
		},
		"await": true,
	}
}

// prefillFromHostResult 把宿主改写结果的 Prefill（map[string]any，键=
// target_field_id）转成 form.open-create 命令需要的 prefill 列表
// （[]any{"field_id":..,"text":..}）。nil 值字段跳过（不阻断打开，只是该字段不预填）。
// 🔴 已知行为特点：本函数的输入是 map（JSON 对象，宿主投递载体的既定形状），JSON 对象本身不携带顺序，故按 field_id 字典序输出——确定性
// 但顺序可能与用户在面板里配置的顺序不同，这是宿主 map 投递形状带来的已知、可接受
// 的行为差异（前端按 field_id 渲染字段本就与顺序无关）。
func prefillFromHostResult(hostPrefill map[string]any) []any {
	fieldIDs := make([]string, 0, len(hostPrefill))
	for fieldID := range hostPrefill {
		fieldIDs = append(fieldIDs, fieldID)
	}
	sort.Strings(fieldIDs)
	out := make([]any, 0, len(fieldIDs))
	for _, fieldID := range fieldIDs {
		v := hostPrefill[fieldID]
		if v == nil {
			continue
		}
		out = append(out, map[string]any{"field_id": fieldID, "text": fmt.Sprint(v)})
	}
	return out
}

// ── 新建填充链（create / 找不到时新建）──────────────────────────────────────

// buildFillEffect 组装 form.open-create 效果（本函数是「宿主未回执 create 结果」的兜底路径，
// 正常链路命中时 Execute 直接走 buildFillEffectFromHostPrefill，不经过这里）：
//  1. 从 linked_form_v4.prefill 取填充映射（目标字段 ← 表达式）；无映射则零预填直接开弹窗；
//  2. 映射非空即代表本次派发本该有宿主算好的 create 结果却未回执（要么本次
//     派发本就未命中 V4 派发器，要么是「open 0 命中转新建」级联但宿主 prefill 键缺席，
//     见 Execute 里的调用处注释）——直接报具名错误 LINKED_FORM_V1_UNSUPPORTED_CREATE，
//     不做静默回落。原「填充表达式只能引用本表单字段」的静态语义校验
//     （firstNonContextAliasInOutputs，读旧版 query 键）已整体移除：V4 下该约束改由
//     宿主保存闸在设计期拒绝，插件侧不再读新文档重复实现同一条规则。
//     LINKED_FORM_FILL_BAD_REF 错误码保留常量含义与四语言 locale 条目（V4 起由宿主
//     保存闸承担，本函数不再有它的产生点），不删多语言；
//  3. 组装 form.open-create 命令（弹窗填报，await 回执续段；writeback=「保存后写回」步骤配置，
//     规整后原样透传——真正写回由前端保存成功后调宿主写回端点，服务端按节点白名单反查鉴权）。
func (s *Service) buildFillEffect(
	ctx context.Context,
	loc string,
	nodeID string,
	linkedFormV4Map map[string]any,
	templateID string,
	autoClose bool,
	writebackRaw any,
) (map[string]any, *bizpb.ExecuteResponse) {
	outputs := fillOutputs(linkedFormV4Map)

	if len(outputs) > 0 {
		return nil, failure(loc, "LINKED_FORM_V1_UNSUPPORTED_CREATE", "linked_form.v1_unsupported_create", map[string]any{
			"node_id":     nodeID,
			"template_id": templateID,
		})
	}

	// 走到这里 outputs 恒为空（非空已在上面提前返回），零预填直接开弹窗。
	prefill := []any{}
	logf(ctx, "INFO", "联动表单(新建)：零填充映射，直接打开填报弹窗，target_template_id=%s", templateID)
	return map[string]any{
		"kind":    "command",
		"command": "form.open-create",
		"args": map[string]any{
			"target_template_id": templateID,
			"prefill":            prefill,
			"auto_close_on_save": autoClose,
			// writeback：保存后写回配置（沿用原 fill_form 契约），规整透传给前端 openFormFill 链；
			// binding_id 留空——宿主写回端点支持按 node_id 全文档搜索定位。
			"writeback":  normalizeWriteback(writebackRaw),
			"binding_id": "",
			"node_id":    nodeID,
		},
		// E1 回执续段：填报弹窗关闭（保存或取消）才算语义完成，需前端回执引擎恢复后续。
		"await": true,
	}, nil
}

// normalizeWriteback 把 params.writeback（自由格式 map）规整为 command args 里透传的
// {enabled, mappings:[{source_field_id,target_field_id,target_data_table_id}]} 形态：缺失/非法
// 一律安全兜底（enabled=false、mappings=[]），映射项缺必填 id 的整项丢弃，不 panic
// （平移自原 fillform.normalizeWriteback，契约零改动）。
func normalizeWriteback(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	enabled := false
	if v, ok := m["enabled"].(bool); ok {
		enabled = v
	}
	mappingsRaw, _ := m["mappings"].([]any)
	mappings := make([]any, 0, len(mappingsRaw))
	for _, item := range mappingsRaw {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sourceFieldID, _ := im["source_field_id"].(string)
		targetFieldID, _ := im["target_field_id"].(string)
		sourceFieldID = strings.TrimSpace(sourceFieldID)
		targetFieldID = strings.TrimSpace(targetFieldID)
		if sourceFieldID == "" || targetFieldID == "" {
			continue
		}
		targetDataTableID, _ := im["target_data_table_id"].(string)
		mappings = append(mappings, map[string]any{
			"source_field_id":      sourceFieldID,
			"target_field_id":      targetFieldID,
			"target_data_table_id": strings.TrimSpace(targetDataTableID),
		})
	}
	return map[string]any{
		"enabled":  enabled,
		"mappings": mappings,
	}
}

// fillOutputs 从 linked_form_v4.prefill 取填充映射列表（nil 安全；文档缺失/非法时返回空）。
// V4 文档把填充映射放在顶层 prefill 字段（与旧版 query.target.outputs 同构，
// 只是容器换成了整份 linked_form_v4 文档，不再嵌在已下线的 query 键内）。
func fillOutputs(linkedFormV4Map map[string]any) []any {
	if linkedFormV4Map == nil {
		return nil
	}
	outputs, _ := linkedFormV4Map["prefill"].([]any)
	return outputs
}

// ── 共用小工具（照 openform/fillform 惯用法）────────────────────────────────

// recordIDFromRow 从查询结果行取注入的 record_id 列（o_record_id），nil/缺失兜底空串，不 panic。
func recordIDFromRow(row map[string]any) string {
	if row == nil {
		return ""
	}
	v, ok := row["o_record_id"]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// stringParam 从 params 取字符串参数，空白/缺失回落默认值。
func stringParam(params map[string]any, key, def string) string {
	v, _ := params[key].(string)
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	return v
}

// actionIDFromQueryContext 从 ctxMap.query_context.action_id 取节点 id（= 引擎侧 input.Node.NodeID，
// buildExecuteContext 恒注入）。缺失/非字符串安全兜底空串，不 panic。
func actionIDFromQueryContext(ctxMap map[string]any) string {
	qc, _ := ctxMap["query_context"].(map[string]any)
	if qc == nil {
		return ""
	}
	id, _ := qc["action_id"].(string)
	return id
}

// effectsToAny 把 []map[string]any 转成 structpb.NewStruct 期望的 []any 形态。
func effectsToAny(effects []map[string]any) []any {
	out := make([]any, 0, len(effects))
	for _, e := range effects {
		out = append(out, e)
	}
	return out
}

// contextMap：nil 安全的顶层转换（照 extract 惯用法）。
func contextMap(ctx *structpb.Struct) map[string]any {
	if ctx == nil {
		return map[string]any{}
	}
	return ctx.AsMap()
}

// failure 构造带 i18n 错误契约的失败响应（照 extract/openform 惯用法）。
func failure(loc, code, key string, params map[string]any) *bizpb.ExecuteResponse {
	return biz.Fail(code, key, tr.T(loc, key, params))
}

// logf 在关键位置按级别把日志上报给宿主（CLAUDE.md 要求）：优先经 SDK 分级日志器 host.Logger()；
// host 未注入（裸进程/无回连令牌）时 fallback 仅写 stderr，不阻断。
func logf(ctx context.Context, level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if h := plugin.HostFrom(ctx); h != nil {
		if lg := h.Logger(); lg != nil {
			lg.Emit(ctx, strings.ToLower(level), "plugin", "linked-form", "", msg, nil)
			return
		}
	}
	log.Printf("[linked-form][%s] %s", level, msg)
}
