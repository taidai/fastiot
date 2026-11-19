package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"fastiot/internal/app"
	"fastiot/internal/backup"
)

var version = "development"

const usage = `用法：
  fastiot [选项]                 启动实例
  fastiot backup -data <目录> -out <备份.tar.gz>
  fastiot restore -from <备份.tar.gz> -data <目录>

备份与恢复是离线操作：必须先停止实例。恢复会整体替换数据目录，
并用 recovery.json 记录快照时间与快照之后的数据损失窗口；它不是项目
版本回退（版本回退只重放工程配置，历史、报警与审计保留）。
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackup(os.Args[2:])
			return
		case "restore":
			runRestore(os.Args[2:])
			return
		case "help", "-h", "--help":
			fmt.Print(usage)
			return
		}
	}
	serve()
}

func serve() {
	port := flag.String("port", "8080", "HTTP port")
	dataDir := flag.String("data", "data", "data directory")
	natsPort := flag.Int("nats-port", 4222, "embedded NATS port")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.Start(ctx, app.Config{
		DataDir:  *dataDir,
		HTTPPort: *port,
		NATSPort: *natsPort,
		Version:  version,
	})
	if err != nil {
		log.Fatal(err)
	}

	select {
	case <-ctx.Done():
	case <-application.Exited():
	}
	if err := application.Close(); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func runBackup(args []string) {
	flags := flag.NewFlagSet("backup", flag.ExitOnError)
	dataDir := flags.String("data", "data", "停止状态的实例数据目录")
	out := flags.String("out", "", "写入的备份归档路径（.tar.gz）")
	_ = flags.Parse(args)
	if *out == "" {
		log.Fatal("backup: -out 是必填项")
	}
	manifest, err := backup.Create(*dataDir, *out, version)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(`备份完成：%s
快照时间：%s
文件：%d（%d 字节）
审计记录：%d 条%s
报警事件：%d 条%s
项目版本：%d 个（%d 个站点）
包含账号、设备密钥与历史存储；快照之后产生的数据不在其中。
`, *out, manifest.CreatedAt.Local().Format("2006-01-02 15:04:05"), manifest.Files, manifest.Bytes,
		manifest.Inventory.AuditRecords, rangeText(manifest.Inventory.AuditFirst, manifest.Inventory.AuditLast),
		manifest.Inventory.AlarmEvents, rangeText(manifest.Inventory.AlarmFirst, manifest.Inventory.AlarmLast),
		manifest.Inventory.ProjectVersions, manifest.Inventory.ProjectSites)
}

func runRestore(args []string) {
	flags := flag.NewFlagSet("restore", flag.ExitOnError)
	dataDir := flags.String("data", "data", "要替换的实例数据目录")
	from := flags.String("from", "", "备份归档路径")
	_ = flags.Parse(args)
	if *from == "" {
		log.Fatal("restore: -from 是必填项")
	}
	recovery, err := backup.Restore(*from, *dataDir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(`恢复完成：%s
快照时间：%s
恢复时间：%s
数据损失：快照之后到恢复之前产生的历史、报警、审计与工程变更不在本实例中（%s 至 %s）
原数据目录：%s
%s
`, *dataDir,
		recovery.SnapshotAt.Local().Format("2006-01-02 15:04:05"),
		recovery.RestoredAt.Local().Format("2006-01-02 15:04:05"),
		recovery.DataLoss.After.Local().Format("2006-01-02 15:04:05"),
		recovery.DataLoss.Before.Local().Format("2006-01-02 15:04:05"),
		replacedText(recovery.ReplacedDataDir), recovery.NotVersionRollback)
}

func rangeText(first, last string) string {
	if first == "" {
		return ""
	}
	if last == "" || last == first {
		return "（" + first + "）"
	}
	return "（" + first + " 至 " + last + "）"
}

func replacedText(path string) string {
	if path == "" {
		return "无（目标目录原本为空或不存在）"
	}
	return path + "（未删除）"
}
