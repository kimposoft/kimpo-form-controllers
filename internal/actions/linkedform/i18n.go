// i18n.go — 插件后端多语言（两级查找方案，见 SDK docs/02-i18n-contract.md）。
// 语言文件随二进制 go:embed 打包（无需进 .kpp）；查找链：
// 透传 locale → 默认 zh-CN → 空串（空串时宿主按 message_key 经中央库兜底重译）。
package linkedform

import (
	"embed"

	kimpoi18n "github.com/kimposoft/kimpo-plugin-sdk/i18n"
)

//go:embed locales/*.json
var localeFS embed.FS

// tr 是插件内嵌翻译器（构造后只读，并发安全）。默认 locale=zh-CN。
var tr = kimpoi18n.MustNew(localeFS, "locales", "zh-CN")
