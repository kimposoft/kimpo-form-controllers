#!/usr/bin/env bash
# 从源码构建并打包本插件：bash build.sh
#
# 步骤：go vet → go test → go build plugin.bin → 组装 .kpp → kimpo-plugin-sign lint →（可选）签名。
# 产物：release/<plugin_id>-<version>-<GOOS>-<GOARCH>.kpp（ZIP，manifest.json 位于包根）。
# 包内：manifest.json / plugin.bin / frontend/locales/（插件语言包）/ capability-knowledge/（动作能力说明）
#
# 环境变量：
#   GOOS / GOARCH                  交叉编译目标；plugin.bin 必须与 Kimpo 宿主运行的平台一致（默认本机）
#   KIMPO_PLUGIN_SIGNING_KEY_FILE  私钥文件路径；设置了就在打包后签名（可选）
#   KIMPO_PLUGIN_LINT_FLAGS        追加给 kimpo-plugin-sign lint 的参数（可选）
#   KIMPO_PLUGIN_SKIP_LINT=1       跳过打包后的 manifest 离线预检
#
# 注意：manifest.json 里的插件 ID 属于 Kimposoft 保留命名空间，官方签名只由 Kimposoft 发布。
# 自行构建的包请先把 plugin_id（以及 i18n.namespace、capability-knowledge/provider.json 的 provider）
# 改成你自己的插件 ID，再以第三方身份安装；否则 lint 会报保留命名空间错误。
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"

# 插件是独立模块：关闭 go.work，按 go.mod 里的发布版本从模块代理拉取 SDK（不写 replace）。
export GOWORK=off
SDK_VERSION=$(awk '$1=="github.com/kimposoft/kimpo-plugin-sdk"{print $2; exit}' go.mod)
[ -n "$SDK_VERSION" ] || { echo "go.mod 里找不到 github.com/kimposoft/kimpo-plugin-sdk 依赖" >&2; exit 1; }
# manifest.sdk_version（不带 v）应等于构建所用的 SDK 版本：宿主按它决定兼容口径
MANIFEST_SDK=$(python3 -c 'import json;print(json.load(open("manifest.json")).get("sdk_version",""))')
if [ "$MANIFEST_SDK" != "${SDK_VERSION#v}" ]; then
  echo "警告：manifest.json sdk_version=$MANIFEST_SDK 与 go.mod 中的 SDK ${SDK_VERSION#v} 不一致，请同步修改" >&2
fi

GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

# ---- 后端：自检在构建机本机架构上跑，交叉编译只作用于 go build ----
env -u GOOS -u GOARCH go vet ./...
env -u GOOS -u GOARCH go test ./...
# -s -w 剥离 DWARF 与符号表（panic 栈仍带函数名与行号）；-trimpath 去掉构建机路径
GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o plugin.bin ./cmd/plugin

# ---- 打包 ----
PLUGIN_ID=$(python3 -c 'import json;print(json.load(open("manifest.json"))["plugin_id"])')
VER=$(python3 -c 'import json;print(json.load(open("manifest.json"))["version"])')
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
cp manifest.json plugin.bin "$STAGE/"
mkdir -p "$STAGE/frontend"
cp -R frontend/locales "$STAGE/frontend/locales"
[ -d capability-knowledge ] && cp -R capability-knowledge "$STAGE/capability-knowledge"
mkdir -p release
OUT="$HERE/release/$PLUGIN_ID-$VER-$GOOS-$GOARCH.kpp"
rm -f "$OUT"
(cd "$STAGE" && zip -qrX "$OUT" .)

# ---- 签名 / 预检工具定位：PATH 里的 kimpo-plugin-sign → 按 SDK 版本 go run ----
SIGN_TOOL=()
if command -v kimpo-plugin-sign >/dev/null 2>&1; then
  SIGN_TOOL=(kimpo-plugin-sign)
else
  SIGN_TOOL=(env GOWORK=off go run "github.com/kimposoft/kimpo-plugin-sdk/cmd/kimpo-plugin-sign@$SDK_VERSION")
fi

# ---- manifest 离线预检（lint）：按宿主安装期规则检查 manifest 与包内文件，error 级问题直接失败 ----
if [ "${KIMPO_PLUGIN_SKIP_LINT:-}" != "1" ]; then
  # shellcheck disable=SC2086
  "${SIGN_TOOL[@]}" lint ${KIMPO_PLUGIN_LINT_FLAGS:-} "$OUT" >&2
fi

# ---- 可选签名：设置了 KIMPO_PLUGIN_SIGNING_KEY_FILE 才签，否则产出未签名包 ----
if [ -n "${KIMPO_PLUGIN_SIGNING_KEY_FILE:-}" ]; then
  "${SIGN_TOOL[@]}" sign -key "$KIMPO_PLUGIN_SIGNING_KEY_FILE" "$OUT" >&2
fi
echo "$OUT"
