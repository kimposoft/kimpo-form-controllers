// Package i18n 提供表单控制器聚合插件共用的后端错误翻译。
// 用户可见错误始终同时携带稳定 message_key 与按触发 locale 生成的 fallback。
package i18n

import (
	"embed"

	"github.com/kimposoft/kimpo-plugin-sdk/direction/biz"
	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
	kimpoi18n "github.com/kimposoft/kimpo-plugin-sdk/i18n"
)

//go:embed locales/*.json
var localeFS embed.FS

var tr = kimpoi18n.MustNew(localeFS, "locales", "zh-CN")

// Fail 构造宿主可二次翻译的失败响应。
func Fail(locale, code, key string, params map[string]any) *bizpb.ExecuteResponse {
	return biz.Fail(code, key, tr.T(locale, key, params))
}
