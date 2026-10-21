# kimpo-form-controllers

Kimpo 官方「表单动作」插件的源码。插件 ID：`com.kimposoft.formcontrollers`。

它是一个动作类（biz 方向）后端插件，为 Kimpo 动作引擎提供两个表单动作：

| 动作 | 名称 | 说明 |
|---|---|---|
| `ui.form_control` | 表单操作 | 保存 / 关闭当前表单，或进入编辑态；与手动点按钮走同一条链路 |
| `ui.linked_form` | 联动表单 | 新建一张目标模板的表单并按表达式预填，或按条件打开已有表单（多条命中时弹选择列表、找不到时可转新建），可配置保存后写回当前表单 |

插件只产出宿主界面命令效果，不持有业务表数据：联动表单的查询与预填求值由宿主在派发动作前完成，经 `query_context` 回执给插件（SDK `plugin.ParseLinkedFormResult`）；字段 ID 来自宿主公开字段目录，当前表单值以宿主为准。

## 依赖

- Go ≥ 1.23
- Kimpo 插件 SDK：Go 模块 [`github.com/kimposoft/kimpo-plugin-sdk`](https://github.com/kimposoft/kimpo-plugin-sdk) `v1.1.0-tp.20`（预览版，见 `go.mod`；不使用 `replace` / `go.work`）
- 打包与预检：`zip`、`python3`，以及 SDK 自带的签名 / 预检工具 `kimpo-plugin-sign`

## 从源码构建 .kpp

```sh
# 可选：先装好预检工具（不装时 build.sh 会用 go run 临时编译）
GOWORK=off go install github.com/kimposoft/kimpo-plugin-sdk/cmd/kimpo-plugin-sign@v1.1.0-tp.20

bash build.sh                          # 本机平台
GOOS=linux GOARCH=amd64 bash build.sh  # 实例跑在 Linux x86-64 服务器上时
```

`build.sh` 依次执行 `go vet` → `go test` → `go build`（产出 `plugin.bin`）→ 组装 `.kpp` → `kimpo-plugin-sign lint`，产物在 `release/<插件ID>-<版本>-<GOOS>-<GOARCH>.kpp`。`plugin.bin` 必须与 Kimpo 实例运行的平台一致。只想跑单测：`make test`。

包内结构：`manifest.json`、`plugin.bin`、`frontend/locales/`（插件语言包）、`capability-knowledge/`（动作能力说明，供 Kimpo 内的 AI 助手检索）。

## 关于签名与插件 ID

- `com.kimposoft.*` / `com.kimpo.*` 是 Kimposoft 保留命名空间。**官方签名的安装包只由 Kimposoft 发布**，本仓库不含任何签名私钥。
- 用本仓库源码自行构建的包没有官方签名，宿主会按官方命名空间拦截安装；`kimpo-plugin-sign lint` 也会报 `reserved_plugin_namespace` 错误。
- 如需安装自己构建的版本，请把 `manifest.json` 的 `plugin_id`、`i18n.namespace` 和 `capability-knowledge/provider.json` 的 `provider` 改成你自己的插件 ID（如 `com.example.formcontrollers`），以第三方插件身份安装；签名用你自己的密钥（`KIMPO_PLUGIN_SIGNING_KEY_FILE=<私钥文件> bash build.sh`），流程见 SDK 文档 `docs/third-party/`。
- 本插件申请的数据模型与派发能力属于敏感级权限，第三方身份安装后需管理员在插件授权里补授。

## 许可证

[Apache License 2.0](LICENSE)，Copyright 2026 金宝量行（无锡）软件有限公司，附加声明见 [NOTICE](NOTICE)。
