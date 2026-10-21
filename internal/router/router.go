// Package router 实现表单动作聚合插件的分派层：把 formcontrol / linkedform
// 两个子包按动作名聚合为单一 bizpb.BusinessPluginServer，供单进程 sidecar 贡献两个动作。
// （原 openform「打开表单」/ fillform「填报表单」已于 2026-07-24 合并为 linkedform「联动表单」。）
package router

import (
	"context"

	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
	"github.com/kimposoft/kimpo-form-controllers/internal/actions/formcontrol"
	"github.com/kimposoft/kimpo-form-controllers/internal/actions/linkedform"
	plugini18n "github.com/kimposoft/kimpo-form-controllers/internal/i18n"
)

// Service 路由聚合插件：把表单动作子包（form_control/linked_form）合并为单进程贡献。
// GetActions 拼接各子包动作定义；Execute/ValidateParams 按 req 动作名分派到对应子包。
type Service struct {
	bizpb.UnimplementedBusinessPluginServer
	formcontrol *formcontrol.Service
	linkedform  *linkedform.Service
	// byAction 用动作名索引到子 service，Execute/ValidateParams O(1) 分派。
	// key 直接取各子包导出的 ActionName 常量，避免与 GetActions 里声明的动作名发生字符串漂移。
	byAction map[string]bizpb.BusinessPluginServer
}

// New 构造聚合 service，装配子动作并建好分派表。
func New() *Service {
	fc, lf := formcontrol.New(), linkedform.New()
	svc := &Service{formcontrol: fc, linkedform: lf}
	svc.byAction = map[string]bizpb.BusinessPluginServer{
		formcontrol.ActionName: fc,
		linkedform.ActionName:  lf,
	}
	return svc
}

// GetActions 依次收集各子包的动作定义并拼接返回；任一子调用出错直接透传。
func (s *Service) GetActions(ctx context.Context, req *bizpb.GetActionsRequest) (*bizpb.GetActionsResponse, error) {
	var actions []*bizpb.ActionDefinition

	fcResp, err := s.formcontrol.GetActions(ctx, req)
	if err != nil {
		return nil, err
	}
	actions = append(actions, fcResp.GetActions()...)

	lfResp, err := s.linkedform.GetActions(ctx, req)
	if err != nil {
		return nil, err
	}
	actions = append(actions, lfResp.GetActions()...)

	return &bizpb.GetActionsResponse{Actions: actions}, nil
}

// Execute 按 req.ActionName 分派到对应子包；未知动作名返回统一失败态，不 panic 不静默。
func (s *Service) Execute(ctx context.Context, req *bizpb.ExecuteRequest) (*bizpb.ExecuteResponse, error) {
	sub, ok := s.byAction[req.GetActionName()]
	if !ok {
		return plugini18n.Fail(req.GetTriggerContext().GetLocale(), "ACTION_UNKNOWN", "action.unknown", map[string]any{"action": req.GetActionName()}), nil
	}
	return sub.Execute(ctx, req)
}

// ValidateParams 按 req.ActionName 分派到对应子包；未知动作名返回 Valid:false + 一条校验错误说明。
func (s *Service) ValidateParams(ctx context.Context, req *bizpb.ValidateParamsRequest) (*bizpb.ValidateParamsResponse, error) {
	sub, ok := s.byAction[req.GetActionName()]
	if !ok {
		return &bizpb.ValidateParamsResponse{
			Valid: false,
			Errors: []*bizpb.ValidationError{
				{FieldPath: "action_name", Message: "未知动作: " + req.GetActionName()},
			},
		}, nil
	}
	return sub.ValidateParams(ctx, req)
}
