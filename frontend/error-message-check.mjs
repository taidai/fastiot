import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

const source = readFileSync(new URL("./src/api.ts", import.meta.url), "utf8")
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
})
const { errorMessage } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`)

assert.equal(errorMessage("export_required", 403), "当前账号没有导出权限")
assert.equal(errorMessage("read_only", 403), "该点位只读，不能写入")
assert.equal(errorMessage("node_disabled", 409), "该点位已停用，不能写入")
assert.equal(errorMessage("manual_takeover", 409), "该点位处于人工接管状态，自动写入已拒绝")
assert.equal(errorMessage("invalid_safety_config", 409), "安全配置无效，已拒绝写入；请检查点位安全设置")
assert.equal(
  errorMessage("safety condition soc = 50 does not satisfy gt 80", 409),
  "safety condition soc = 50 does not satisfy gt 80",
)
assert.equal(errorMessage("required input soc is missing", 409), "required input soc is missing")
assert.equal(errorMessage("unknown_backend_error", 409), "操作未完成，请稍后重试")
assert.equal(errorMessage("", 503), "服务暂时不可用，请稍后重试")

console.log("error-message check passed: known codes, safety sentences and fallbacks")
