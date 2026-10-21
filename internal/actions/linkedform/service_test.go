package linkedform

import (
	"testing"
)

// ── buildOpenResult：命中数（≥1）分流纯函数（0 命中兜底在 Execute 调用方）──────

func TestBuildOpenResultSingleHit(t *testing.T) {
	rows := []map[string]any{{"o_record_id": "rec1"}}
	effects := buildOpenResult(rows, nil, "tab", "edit", "tpl1")
	if len(effects) != 1 {
		t.Fatalf("effects = %d, want 1", len(effects))
	}
	if effects[0]["command"] != "form.open" {
		t.Fatalf("command = %v, want form.open", effects[0]["command"])
	}
	args := effects[0]["args"].(map[string]any)
	if args["record_id"] != "rec1" || args["edit_type"] != "edit" || args["target_template_id"] != "tpl1" {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestBuildOpenResultMultiHit(t *testing.T) {
	rows := []map[string]any{
		{"o_record_id": "rec1", "o_fname": "甲"},
		{"o_record_id": "rec2", "o_fname": "乙"},
	}
	effects := buildOpenResult(rows, []string{"fname"}, "tab", "view", "tpl1")
	if effects[0]["command"] != "form.pick-open" {
		t.Fatalf("command = %v, want form.pick-open", effects[0]["command"])
	}
	if effects[0]["await"] != true {
		t.Fatal("pick-open effect must carry await=true")
	}
	args := effects[0]["args"].(map[string]any)
	records := args["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	// 预览列值与列序：records[i].preview 按 previewFieldIDs 取 o_<fid>；args.preview_field_ids 下发列序。
	r0 := records[0].(map[string]any)
	if r0["preview"].(map[string]any)["fname"] != "甲" {
		t.Fatalf("preview mismatch: %v", r0["preview"])
	}
	ids := args["preview_field_ids"].([]any)
	if len(ids) != 1 || ids[0] != "fname" {
		t.Fatalf("preview_field_ids = %v, want [fname]", ids)
	}
}

func TestNormalizePreview(t *testing.T) {
	outputs, ids := normalizePreview([]any{
		map[string]any{"target_field_id": "fa", "expression": map[string]any{"kind": "source_field", "alias": "t1", "field_id": "fa"}},
		map[string]any{"target_field_id": ""},          // 缺字段 id：丢弃
		map[string]any{"target_field_id": "record_id"}, // 撞注入列：丢弃
	})
	if len(outputs) != 1 || len(ids) != 1 || ids[0] != "fa" {
		t.Fatalf("normalizePreview = %v / %v, want 1 项 fa", outputs, ids)
	}
}

func TestNormalizeWriteback(t *testing.T) {
	wb := normalizeWriteback(map[string]any{
		"enabled": true,
		"mappings": []any{
			map[string]any{"source_field_id": "nf", "target_field_id": "cf", "target_data_table_id": "dtc"},
			map[string]any{"source_field_id": "", "target_field_id": "cf2"}, // 缺 source：丢弃
		},
	})
	if wb["enabled"] != true {
		t.Fatal("enabled must be preserved")
	}
	mappings := wb["mappings"].([]any)
	if len(mappings) != 1 {
		t.Fatalf("mappings = %d, want 1", len(mappings))
	}
	m0 := mappings[0].(map[string]any)
	if m0["source_field_id"] != "nf" || m0["target_field_id"] != "cf" || m0["target_data_table_id"] != "dtc" {
		t.Fatalf("mapping mismatch: %v", m0)
	}
	// 缺失/非法整体兜底关。
	empty := normalizeWriteback(nil)
	if empty["enabled"] != false {
		t.Fatal("missing writeback must normalize to disabled")
	}
}

// ── 已移除的旧版校验与查询模型构造 ──────────────────────────────────────────
//
// 填充表达式「不得引用目标表字段」的静态校验已改由宿主保存闸在设计期拒绝；
// 自建打开查询模型 / 求值模型随 V1 查询通道回落分支一并删除，对应用例同步删除。
// 新建填充的 prefill 来源只剩宿主回执（prefillFromHostResult）。
// LINKED_FORM_FILL_BAD_REF 错误码与四语言 locale 条目保留。
