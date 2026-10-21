// Package formcontrol 实现 ui.form_control 动作：对当前表单执行保存/关闭/修改操作。
// 归拢自原 kimpo-form-control 独立插件（sidecar 归拢第二批）。
package formcontrol

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
	plugini18n "github.com/kimposoft/kimpo-form-controllers/internal/i18n"
)

// ActionName 是本子包贡献的唯一动作：表单操作。导出供 router 做动作名分派，避免与
// GetActions 里声明的动作名发生字符串漂移（归拢自 kimpo-form-control 插件）。
const ActionName = "ui.form_control"

// Service 实现 biz.BusinessPlugin：贡献并执行 ui.form_control 动作。
// v1 范围：参数 op（必填字符串，枚举 save/close/edit），按 op 产出对应 command 效果，
// 真正的执行体是前端 editor 里保存/关闭/进编辑按钮的现成 handler（与手动按钮
// 完全同链，不跳过 before-save 动作链、不跳过系统确认提示）。
type Service struct {
	bizpb.UnimplementedBusinessPluginServer
}

func New() *Service { return &Service{} }

// GetActions 声明表单操作动作：参数 op（必填字符串，枚举 save/close/edit）。
func (s *Service) GetActions(_ context.Context, _ *bizpb.GetActionsRequest) (*bizpb.GetActionsResponse, error) {
	schema, err := structpb.NewStruct(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		// x-editor=form-control：设计器据此挂载表单操作专用配置面板（单步骤三卡片选 op），不走通用
		// schema 渲染；旧宿主不识别该关键字时自然回退通用枚举下拉。
		"x-editor": "form-control",
		"properties": map[string]any{
			"op": map[string]any{"type": "string", "enum": []any{"save", "close", "edit"}},
		},
		"required": []any{"op"},
	})
	if err != nil {
		return nil, err
	}
	return &bizpb.GetActionsResponse{
		Actions: []*bizpb.ActionDefinition{
			{
				Name:         ActionName,
				Title:        "表单操作",
				Description:  "对当前表单执行保存/关闭/修改操作（与手动按钮同链：保存不跳过 before-save 动作链，关闭照常弹未保存确认）",
				ParamsSchema: schema,
				Style:        &bizpb.ActionStyle{Shape: "rounded", Color: "info"},
				// 设计定案（ui-actions-six-plugins-design §5.3）：六个界面动作全支持异步；本动作
				// 只触发表单操作视图行为、不直接写业务数据（save 走的现成 handler 自身会写），
				// v1 不接表达式求值。
				Capabilities: &bizpb.ActionCapabilities{Asyncable: true, SideEffecting: false, ExpressionBased: false},
				// 标准动作面板排序权重 90（打开表单之后）。
				SortWeight: 90,
				// 动作面板功能分类（设计器右栏页签聚合依据；标准动作另有宿主 canonical 兜底防旧二进制漂移）。
				Category: "表单",
			},
		},
	}, nil
}

// Execute 读 context.params 的 op，按 save/close/edit 产出对应
// {kind:"command", command:"form.save"|"form.close"|"form.edit"} 效果——真正的执行体在
// 前端 editor（宿主命令白名单路由到保存/关闭/进编辑按钮的现成 handler），本插件只负责
// 声明意图。
func (s *Service) Execute(_ context.Context, req *bizpb.ExecuteRequest) (*bizpb.ExecuteResponse, error) {
	params := extractParams(req.GetContext())
	loc := req.GetTriggerContext().GetLocale()

	op, _ := params["op"].(string)
	op = strings.TrimSpace(op)
	var command string
	switch op {
	case "save":
		command = "form.save"
	case "close":
		command = "form.close"
	case "edit":
		command = "form.edit"
	default:
		return plugini18n.Fail(loc, "FORM_CONTROL_BAD_OP", "form_control.bad_op", nil), nil
	}

	result, err := structpb.NewStruct(map[string]any{
		"effects": []any{
			map[string]any{
				"kind":    "command",
				"command": command,
				"args":    map[string]any{},
				// E1 回执续段（R6b 定案）：save/close/edit 三操作都要等前端 editor 现成 handler
				// 真正跑完（保存态确定/关闭确认结束/进入编辑）才算语义完成，需回执引擎恢复后续。
				"await": true,
			},
		},
	})
	if err != nil {
		return plugini18n.Fail(loc, "FORM_CONTROL_RESULT", "form_control.result_failed", map[string]any{"detail": err}), nil
	}
	return &bizpb.ExecuteResponse{Success: true, Result: result}, nil
}

// ValidateParams 在插件边界再次校验必填枚举，避免调用方绕过宿主轻量 schema 校验后
// 把无效配置保存成运行期假成功。
func (s *Service) ValidateParams(_ context.Context, req *bizpb.ValidateParamsRequest) (*bizpb.ValidateParamsResponse, error) {
	params := map[string]any{}
	if req.GetParams() != nil {
		params = req.GetParams().AsMap()
	}
	var errs []*bizpb.ValidationError
	for key := range params {
		if key != "op" {
			errs = append(errs, &bizpb.ValidationError{FieldPath: key, Message: "不支持的参数"})
		}
	}
	op, _ := params["op"].(string)
	switch strings.TrimSpace(op) {
	case "save", "close", "edit":
	default:
		errs = append(errs, &bizpb.ValidationError{FieldPath: "op", Message: "表单操作只能是 save/close/edit"})
	}
	return &bizpb.ValidateParamsResponse{Valid: len(errs) == 0, Errors: errs}, nil
}

// extractParams 从约定的 context = { "params": {...}, "trigger": {...} } 取出 params 子对象。
func extractParams(ctx *structpb.Struct) map[string]any {
	if ctx == nil {
		return map[string]any{}
	}
	if p, ok := ctx.AsMap()["params"].(map[string]any); ok && p != nil {
		return p
	}
	return map[string]any{}
}
