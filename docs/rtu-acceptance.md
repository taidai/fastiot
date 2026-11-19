# 虚拟串口 RTU 端到端验收（票 #14）

日期：2026-11-20
父规格：issue #1 US 25–27。前置：票 #13 的两泵一水箱样板。

## 结论按证据等级分开

| 等级 | 本次是否执行 | 结论 | 能证明什么 |
| --- | --- | --- | --- |
| 虚拟串口（Linux pty） | **是** | 通过 | 帧结构、CRC、站号处理，以及断线 / 超时 / 迟到回读 / 恢复，在真实字节流和生产 RTU 编解码上成立 |
| 真实串口（本机 USB-RS485 / COM 口） | 否 | **未执行** | 代码路径与命令已就绪（见下），但没有可用的物理串口 |
| 真机（泵站控制器） | 否 | **未执行** | 没有连接任何真实设备 |

**虚拟串口通过不等于真实串口或真机通过。** pty 没有电气层：没有线路噪声、接地回路、
收发方向切换、波特率误差，也没有厂商固件差异。本页任何结论都不能当作现场验收。

## 虚拟串口如何建立

`internal/sim/pumphouse/rtu_linux_test.go`（`//go:build linux`）：

1. `pty.Open()` 建立一对 Linux 伪终端。主端文件句柄交给 `plant.ServeRTU`，作为模拟站的串口；
   从端路径就是主站要打开的“串口设备”。
2. 主站用生产代码 `modbusrt.OpenRTUConfig("从端路径@9600")` 打开；`go.bug.st/serial` 真正
   执行 termios 设置（9600 8N1），随后经 `respreader` 做帧间隔分帧。
3. 两端都用 `modbus.NewRTU` 编解码：请求由主站生成并附加 CRC16，从站校验 CRC 后才执行；
   应答由从站生成并附加 CRC16，主站校验 CRC 后才使用。被模拟的只有“字节从 UART 来”这一点。

非 Linux 平台由 `rtu_other_test.go` 中同名测试显式 `t.Skip` 说明原因，不会静默变成“没有这项覆盖”。

CI（Docker `golang:1.27.3`，`--network none`）可以直接创建 pty，因此这套测试不依赖真实硬件，
可以重复运行。

除测试外，`cmd/pumphouse` 也以同样方式在虚拟串口上完整跑通过一次：模拟站占用一对 pty 的
一端，主站通道占用另一端，中间由纯字节桥（等同于 socat）连接。应用启动后打印
`level=54.7% pressure=0.08 MPa pump1=0 pump2=0`，证明读数来自串口链路；静默 6 秒后
`/link` 的 `Requests` 增加而 `Responses` 不变，解除静默后两者继续同步增长。原始输出见
`evidence/ticket14-rtu-virtual-serial.txt`。

## 验证了哪些协议行为

抓到的真实帧（测试日志）：

```
请求 读保持寄存器 100 起 2 个   01 03 00 64 00 02 85 d4
应答 液位 550 / 压力 8          01 03 04 02 26 00 08 1b 86
```

| 行为 | 怎么验证 | 断言 |
| --- | --- | --- |
| 帧结构 | 从帧日志取原始字节 | 站号、功能码、地址、数量、字节数逐字节匹配；CRC 两字节位置正确 |
| CRC（从站侧） | 注入线路噪声，翻转请求校验字节 | 从站 `CRCErrors` 增加，且**不应答**；主站得到超时，不会拿到错误值 |
| CRC（主站侧） | 注入被翻转的应答校验字节 | 主站报 `CRC error`，该次采集全部无效，不会把坏帧当数据 |
| 站号（请求） | 检查原始请求帧 | 首字节为配置站号 1 |
| 站号（从站过滤） | 用站号 9 的设备轮询同一台从站 | 从站校验后丢弃、保持静默，`IgnoredStation` 增加；主站超时，而不是拿到站 1 的数据 |
| 站号（主站校验应答） | 从站以站号 7 正常应答 | 主站报 `response station 7 does not match request station 1`，不会把别人的应答当作本站数据 |

## 四类异常与恢复

所有故障都由 `RTUFaults` 注入，`SetFaults(RTUFaults{})` 清除；因此每个场景都可以单独、
反复触发（`go test -run <测试名> -count=N`）。

| 场景 | 注入 | 可观察结果 | 恢复 |
| --- | --- | --- | --- |
| 断线 | 主站关闭串口（`unplug`） | 该次采集全部无效，`Stats.Failures` 增加；读取立即失败，不阻塞通道 | 在同一路径重新打开串口并重建通道，下一次采集 health 回到 `healthy`，液位恢复 55% |
| 超时 | `Mute: true`（收下请求不应答） | 采集耗时 ≥ 主站超时（200 ms），结果全部无效；health 变 `degraded`；`LastError` 为 `EOF`（读超时） | 清除故障后下一次采集为 `healthy`，值正确 |
| 迟到回读 | `ResponseDelay: 400 ms`（> 主站 200 ms 超时） | 本次采集超时、结果无效；从站随后仍把迟到帧写上线 | 时钟前进 10 s 使液位从 55.0 变 53.5；清除延时后下一次采集得到 **53.5**，证明迟到的 55.0 帧在发送前被清空，没有被当成新应答 |
| 恢复 | 清除以上故障 | — | 三类故障清除后都能在同一条链路上恢复采集与 `healthy`，无需重启进程 |

迟到回读的结论依赖生产链路已有的行为：`respreader` 在每次 `Write` 前清空输入缓冲。
测试把“迟到帧已上线”作为等待条件，所以结果是确定的，不是靠睡眠碰运气。

## 复现

```powershell
docker run --rm --network none -v "${PWD}:/src:ro" -v fastems-gomod:/go/pkg/mod -v fastiot-go-build:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.27.3 go test -count=1 -v -run TestRTUVirtualSerial ./internal/sim/pumphouse
```

测试清单（先失败后通过；最初的失败是编译失败，见 `evidence/ticket14-rtu-virtual-serial.txt`）：

- `TestRTUVirtualSerialFramesCRCAndStation`
- `TestRTUVirtualSerialTimeoutAndRecovery`
- `TestRTUVirtualSerialLateReadback`
- `TestRTUVirtualSerialDisconnectAndRecover`
- `TestRTUVirtualSerialResponseStationGuard`
- `TestRTUVirtualSerialCRCRejection`
- `TestResponseStationGuard`（modbusrt 单元测试：主站应答站号校验）

## 真实串口（未执行）

`cmd/pumphouse` 的 RTU 从站与通道代码路径已在虚拟串口上执行过（见上），但**没有**在任何
物理串口上执行。需要一对串口（虚拟或物理），一端给模拟站，一端给主站：

```bash
socat -d -d pty,raw,echo=0,link=/tmp/pump-sim pty,raw,echo=0,link=/tmp/pump-bus
go run ./cmd/pumphouse -data /tmp/pump-data -sim-rtu /tmp/pump-sim@9600 -rtu-bus /tmp/pump-bus@9600
```

Windows 上可用 com0com 建立成对 COM 口，或把 `-sim-rtu COM5@9600` 指向 USB-RS485 适配器、
`-rtu-bus` 指向主站适配器。真实串口执行前必须换成空数据目录，并在仓库外确认端口不被占用。
本次没有物理串口，因此上面两条命令**没有实际执行**。

运行后可重复触发同样的链路故障（GET 查看计数）：

```bash
curl http://127.0.0.1:15021/link
curl -X POST http://127.0.0.1:15021/link -d '{"mute":true}'
curl -X POST http://127.0.0.1:15021/link -d '{"delay_ms":400}'
curl -X POST http://127.0.0.1:15021/link -d '{"mute":false,"delay_ms":0}'
```

真机只有在把上述链路换成现场设备、并由现场人员确认点位与安全逻辑后才能另立证据。

## 边界与未做

- 只有一台从站在线上；多站总线冲突、波特率误差、半双工时序没有验证。
- 迟到回读只在“迟到帧先于下一次发送到达”的窗口内验证；若迟到帧恰好落在下一次应答窗口，
  RTU 协议本身无法区分，本验收不声称覆盖该情形。
- 没有电气层证据；真实串口与真机均未执行。
- 没有做长稳测试（数小时丢包率、错误分布）。
