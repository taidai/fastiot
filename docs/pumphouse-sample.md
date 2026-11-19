# 两泵一水箱泵房样板（不安装 EMS）

状态：软件样板，使用**明确标注的模拟设备**。不是真机验收，也没有经过虚拟串口 RTU
（另一张票）或真实 RS485 链路。所有结论只覆盖软件范围。

## 这个样板证明什么

- 只用开源底座能力建立现场：Modbus TCP 采集、点位表、设备模型与命名命令、命令回读确认、
  安全判决、可配置报警、历史和运行画面。
- 不需要安装 EMS 行业包，也不创建任何能源对象（Site/Plant/EnergyUnit/能力规则）。
- 模拟站不是固定数值的读写桩：
  - 启动有过程：写启停命令后约 2 秒电机运行，运行反馈再滞后约 0.8 秒。
  - 停止有过程：写停止后约 1.5 秒反馈才消失。
  - 就地模式：命令被协议接受，但现场开关忽略它；运行反馈不变，命令最终为“未知”。
  - 故障：启动命令被设备以 Modbus 异常拒绝，命令结果为“失败”，运行反馈始终不变成运行。
- 液位、压力随泵运行和排水真实变化，历史与画面都来自同一条采集路径。

## 运行

准备：Go 1.27.3（或按仓库的 Docker 方式构建）。**使用一个空的数据目录**，不要指向已有现场数据。
干净检出首次构建前需要先生成内嵌前端产物（仓库既有要求，否则 `internal/web` 的
`//go:embed all:dist` 会失败）：

```powershell
npm --prefix frontend ci
npm --prefix frontend run build
```

然后启动样板：

```powershell
go run ./cmd/pumphouse -data .\pumphouse-data -port 8080 -nats-port 4222
```

也可以先构建：

```powershell
go build -o pumphouse.exe ./cmd/pumphouse
./pumphouse.exe -data .\pumphouse-data
```

启动后：

- 应用：`http://127.0.0.1:8080/`，账号 `admin`，默认演示密码 `pumphouse-demo-2026`
  （可用 `-password` 或 `FASTEMS_ADMIN_PASSWORD` 覆盖；至少 12 位）。
- 模拟站：Modbus TCP `127.0.0.1:15020`（`-sim-port` 可改）。
- 模拟站控制口：`http://127.0.0.1:15021/`（`-control` 可改），只用于演示故障与就地状态。

常用参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-data` | `pumphouse-data` | 数据目录；空目录才是“从零建立” |
| `-port` | `8080` | 应用 HTTP 端口 |
| `-nats-port` | `4222` | 内嵌 NATS 端口 |
| `-sim-port` | `15020` | 模拟站 Modbus TCP 端口 |
| `-control` | `127.0.0.1:15021` | 模拟站故障/就地注入口 |
| `-password` | `pumphouse-demo-2026` | 首次初始化密码 |

数据目录里会写入 `profiles/pumphouse-sim.json`（模拟设备的自定义 Profile）。重复启动同一个
数据目录时，程序检测到泵房分组已存在就只打印入口，不会重复建设备。

## 程序建立了哪些对象

全部通过产品 HTTP API 建立，等价于交付人员手工操作：

- **分组**：`泵房样板（模拟设备）`。
- **Modbus TCP 通道**：`泵站模拟器（Modbus TCP，非真实设备）`，站号 1，轮询 1 秒。
- **点位表（14 点）**：
  - 保持寄存器：水箱液位（0.1%）、出口压力（0.01 MPa）、两台泵电流（0.1 A）、
    两台泵启停命令、两台泵启动次数。
  - 离散输入：两台泵运行反馈、两台泵远程模式（1 = 远程，0 = 就地）、两台泵故障。
- **设备模型与命名命令**：绑定 `pumphouse-sim` Profile，命令 `start-1` / `stop-1` /
  `start-2` / `stop-2`。命令写入启停命令寄存器，**确认条件是对应泵的运行反馈**，不是
  命令字自身；所以“写进去了”不等于“泵起来了”。
- **运行画面**：4 列 10 块：液位、压力、两台泵运行反馈、两台泵就地/远程、两台泵故障、
  两台泵电流。
- **报警类型**：1#/2# 泵故障（严重，立即）；水箱液位低（警告，低于 20% 持续 5 秒）。
- **安全判决**：两台泵的启停命令点上都带“液位 > 5%”条件；液位过低时命令在下发前被拒绝。

## 可观察结果

1. 打开 `画面`，节点选 `泵房样板（模拟设备）`。液位和压力每 1 秒刷新。
2. 在页头点 `常用命令`，找到站号 1 的设备，执行 `启动 1#泵`：
   - 命令先为 `accepted`；
   - 约 2 秒内“1#泵运行反馈”仍为 0；
   - 反馈变为 1 后命令才变成 `confirmed`，同时电流、压力上升，液位开始上升；
   - 停止同理，反馈保持约 1.5 秒后才消失。
3. 故障拒动：

   ```powershell
   curl.exe -X POST http://127.0.0.1:15021/pump/1/fault -H "Content-Type: application/json" -d '{\"active\":true}'
   ```

   “1#泵故障”变 1，报警出现；再执行 `启动 1#泵`，命令结果为 `failed`，原因包含
   `device refused write`（Modbus 异常），运行反馈不会变成 1。清除故障：

   ```powershell
   curl.exe -X POST http://127.0.0.1:15021/pump/1/fault -H "Content-Type: application/json" -d '{\"active\":false}'
   ```

   清除故障不会自己启泵，必须重新下发命令。

4. 就地拒动：

   ```powershell
   curl.exe -X POST http://127.0.0.1:15021/pump/1/local -H "Content-Type: application/json" -d '{\"active\":true}'
   ```

   “1#泵远程模式”变 0（就地）。此时下发 `启动 1#泵`：写操作被接受，但泵不动，命令在
   超时后为 `unknown`（可能已执行也可能没有），运行反馈始终为 0。这就是“就地拒动”，
   不是静默成功。

   ```powershell
   curl.exe -X POST http://127.0.0.1:15021/pump/1/local -H "Content-Type: application/json" -d '{\"active\":false}'
   ```

5. 模拟站当前状态可直接查看：

   ```powershell
   curl.exe http://127.0.0.1:15021/state
   ```

6. 历史：`历史` 页选择液位或运行反馈点位，能看到启停期间的连续样本。
7. 安全判决：水箱无人启泵时会持续排水。液位降到 5% 以下后，启泵命令会在下发前被安全
   条件拒绝（结果 `failed`，原因含 `safety condition`）。重新启动样板会把模拟水箱恢复到
   初始液位；这是模拟站状态，不写入工程配置。

## 验证代码

- `internal/sim/pumphouse`：过程模型、`channel.Bus` 适配、Modbus TCP 服务器、样板的
  点位/命令/画面/报警定义。单元测试覆盖启动过程、反馈滞后、停止过程、故障跳闸与拒绝、
  就地忽略、液位动态和真实 Modbus TCP 帧。
- `internal/appapi/pumphouse_integration_test.go`：用真实实例、真实运行时、真实 HTTP API
  建立样板，断言启动的中间态与反馈延迟、停止延迟、故障拒绝并报警、就地超时为未知。

运行：

```powershell
docker run --rm --network none -v "${PWD}:/src:ro" -v fastems-gomod:/go/pkg/mod -v fastiot-go-build:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.27.3 go test -count=1 ./internal/sim/pumphouse
```

```powershell
docker run --rm --network none -v "${PWD}:/src:ro" -v fastems-gomod:/go/pkg/mod -v fastiot-go-build:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.27.3 go test -count=1 -run TestPumpHouseSampleCommandLifecycle ./internal/appapi
```

## 不覆盖的范围

- 虚拟串口上的 RTU 帧/CRC/站号端到端验收（另一张票）。
- 真实串口、RS485 和目标泵站真机。
- 自动液位控制规则；本样板只做监控与受控启停。
- 独立用户按文档交付的可用性试用。
