// Form Controllers Plugin —— 动作引擎的表单动作聚合插件。
// 单进程贡献两个表单类界面动作：表单操作 ui.form_control / 联动表单 ui.linked_form
// （由原打开表单 ui.open_form 与填报表单 ui.fill_form 合并而成）。多个动作合并在一个插件
// 进程内提供，减少常驻 sidecar 数量。
//
// linked_form 执行态经 plugin.HostFrom(ctx) 校验宿主句柄，并只消费宿主预先写入
// query_context 的宿主 V4 派发结果（alice_linked_form_result）；聚合后 host 仍由 SDK 按每次调用的 ctx 注入。
package main

import (
	"log"

	"github.com/kimposoft/kimpo-plugin-sdk/direction/biz"
	"github.com/kimposoft/kimpo-plugin-sdk/plugin"
	formcontrollers "github.com/kimposoft/kimpo-form-controllers"
	"github.com/kimposoft/kimpo-form-controllers/internal/router"
)

func main() {
	opts := plugin.ParseFlags()
	// 二进制与宿主扫描共享 tracked 根 manifest.json 这一份权威，不生成或读取 cmd 目录副本。
	opts.Manifest = formcontrollers.ManifestJSON()
	if err := plugin.Serve(opts, biz.Register(router.New())); err != nil {
		log.Fatalf("[form-controllers] %v", err)
	}
}
