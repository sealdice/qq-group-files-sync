package main

// 一个用于调试 OneBot11 适配器的简单入口
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/sealdice/smallseal/adapters"
	"github.com/sealdice/smallseal/dice"
	"github.com/sealdice/smallseal/dice/types"

	"go.uber.org/zap"
)

type ob11Callback struct {
	dice *dice.Dice
}

func (cb *ob11Callback) OnError(err error) {
	fmt.Printf("OnError: %v\n", err)
	debug.PrintStack()
}

var (
	conn      *adapters.PlatformAdapterOB11
	config    *AppConfig
	fsManager *FileSystemManager
)

func (cb *ob11Callback) OnMessageReceived(info *adapters.MessageSendCallbackInfo) {
	_, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("OnMessageReceived: marshal err, info=%v, err=%v\n", info, err)
		return
	}
	// fmt.Printf("OnMessageReceived: %s \n", string(jsonInfo))

	if strings.EqualFold(info.Message.Segments.ToText(), ".同步当前") {
		// 判断发送者不是自己，是自己就退出
		// 如果 sender 是空的 就是自己
		if info.Message.Sender.UserID == "" {
			return
		}

		// 调用封装的同步文件函数，同步当前群组
		syncGroupFilesCommand(info.Message.GroupID, info.Message.GroupID)
	}

	// 检查是否是 ".同步文件 QQ-Group:xxx" 格式的消息
	messageText := info.Message.Segments.ToText()
	if strings.HasPrefix(messageText, ".同步文件 ") {
		// 判断发送者不是自己，是自己就退出
		// 如果 sender 是空的 就是自己
		if info.Message.Sender.UserID == "" {
			return
		}

		// 提取群组ID
		parts := strings.Fields(messageText)
		if len(parts) != 2 {
			conn.MsgSendToGroup(&adapters.MessageSendRequest{
				TargetId: info.Message.GroupID,
				Segments: []types.IMessageElement{
					&types.TextElement{Content: "格式错误，请使用: .同步文件 QQ-Group:群号"},
				},
			})
			return
		}

		targetGroupID := parts[1]
		// 验证群组ID格式
		if !strings.HasPrefix(targetGroupID, "QQ-Group:") {
			conn.MsgSendToGroup(&adapters.MessageSendRequest{
				TargetId: info.Message.GroupID,
				Segments: []types.IMessageElement{
					&types.TextElement{Content: "群组ID格式错误，请使用: QQ-Group:群号"},
				},
			})
			return
		}

		// 调用封装的同步文件函数
		syncGroupFilesCommand(targetGroupID, info.Message.GroupID)
	}

	if strings.EqualFold(info.Message.Segments.ToText(), ".同步全部") {
		// 判断发送者不是自己，是自己就退出
		if info.Message.Sender.UserID == "" {
			return
		}

		// 调用封装的同步全部函数
		syncAllGroupsCommand(info.Message.GroupID)
	}

	if strings.EqualFold(info.Message.Segments.ToText(), ".展示页面") {
		if info.Message.Sender.UserID == "" {
			return
		}

		// 生成HTML展示页面
		err := GenerateDashboard(config, fsManager)
		if err != nil {
			fmt.Printf("生成展示页面失败: %v\n", err)
			conn.MsgSendToGroup(&adapters.MessageSendRequest{
				TargetId: info.Message.GroupID,
				Segments: []types.IMessageElement{
					&types.TextElement{Content: fmt.Sprintf("生成展示页面失败: %v", err)},
				},
			})
			return
		}

		// 获取生成的HTML文件路径
		outputFile := config.Web.DashboardFile
		if outputFile == "" {
			outputFile = "index.html"
		}

		// 获取文件系统基础路径
		basePath := fsManager.GetBasePath()
		fullPath := filepath.Join(basePath, outputFile)

		fmt.Printf("展示页面生成完成: %s\n", fullPath)
		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("展示页面生成完成，已保存到: %s", fullPath)},
			},
		})
	}
	// if cb.dice != nil && info.Message != nil {
	// 	cb.dice.Execute("", info.Message)
	// }
}

func (cb *ob11Callback) OnEvent(evt *adapters.AdapterEvent) {
	if evt.Type == "heartbeat" {
		return
	}

	jsonEvt, err := json.Marshal(evt)
	if err != nil {
		fmt.Printf("OnEvent: marshal err, evt=%v, err=%v\n", evt, err)
		return
	}
	fmt.Printf("OnEvent: %s\n", string(jsonEvt))
}

// syncSingleGroup 内部函数，执行单个群组的同步操作
func syncSingleGroup(targetGroupID string) error {
	helper := NewGroupFileHelper(conn, fsManager)
	return helper.SyncGroupFiles(targetGroupID)
}

// syncGroupFilesCommand 同步指定群组的文件
func syncGroupFilesCommand(targetGroupID, sourceGroupID string) {
	conn.MsgSendToGroup(&adapters.MessageSendRequest{
		TargetId: sourceGroupID,
		Segments: []types.IMessageElement{
			&types.TextElement{Content: fmt.Sprintf("即将开始同步群 %s 的文件", targetGroupID)},
		},
	})

	// 调用内部同步函数
	err := syncSingleGroup(targetGroupID)
	if err != nil {
		fmt.Printf("同步群文件失败: %v\n", err)
		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: sourceGroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("同步群 %s 文件失败: %v", targetGroupID, err)},
			},
		})
		return
	}

	if err := GenerateDashboard(config, fsManager); err != nil {
		zap.S().Warnf("failed to build dashboard: %v", err)
	}

	basePath := fsManager.GetBasePath()
	fmt.Printf("群 %s 文件同步完成，保存到: %s\n", targetGroupID, basePath)
	conn.MsgSendToGroup(&adapters.MessageSendRequest{
		TargetId: sourceGroupID,
		Segments: []types.IMessageElement{
			&types.TextElement{Content: fmt.Sprintf("群 %s 文件同步完成，已保存到: %s", targetGroupID, basePath)},
		},
	})
}

// syncAllGroupsCommand 同步所有配置的群组文件
func syncAllGroupsCommand(sourceGroupID string) {
	// 检查配置文件中是否有群组列表
	if len(config.Groups) == 0 {
		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: sourceGroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: "配置文件中没有配置群组列表，请在 config.yaml 中添加 groups 配置"},
			},
		})
		return
	}

	conn.MsgSendToGroup(&adapters.MessageSendRequest{
		TargetId: sourceGroupID,
		Segments: []types.IMessageElement{
			&types.TextElement{Content: fmt.Sprintf("即将开始同步 %d 个群组的文件", len(config.Groups))},
		},
	})

	// 遍历配置中的所有群组进行同步
	successCount := 0
	failedGroups := []string{}

	for _, group := range config.Groups {
		fmt.Printf("开始同步群组: %s (%s)\n", group.ID, group.Alias)

		// 调用内部同步函数，避免发送过多消息
		err := syncSingleGroup(group.ID)
		if err != nil {
			fmt.Printf("同步群组 %s 失败: %v\n", group.ID, err)
			failedGroups = append(failedGroups, group.Alias)
		} else {
			successCount++
		}
	}

	// 生成仪表板
	if err := GenerateDashboard(config, fsManager); err != nil {
		zap.S().Warnf("failed to build dashboard: %v", err)
	}

	// 发送同步结果
	resultMsg := fmt.Sprintf("同步全部完成！成功: %d 个群组", successCount)
	if len(failedGroups) > 0 {
		resultMsg += fmt.Sprintf("，失败: %d 个群组 (%s)", len(failedGroups), strings.Join(failedGroups, ", "))
	}

	basePath := fsManager.GetBasePath()
	resultMsg += fmt.Sprintf("\n文件已保存到: %s", basePath)

	conn.MsgSendToGroup(&adapters.MessageSendRequest{
		TargetId: sourceGroupID,
		Segments: []types.IMessageElement{
			&types.TextElement{Content: resultMsg},
		},
	})
}

// syncAllGroupsCLI 命令行版本的同步所有群组文件函数
func syncAllGroupsCLI() error {
	// 检查配置文件中是否有群组列表
	if len(config.Groups) == 0 {
		fmt.Println("配置文件中没有配置群组列表，请在 config.yaml 中添加 groups 配置")
		return fmt.Errorf("没有配置群组列表")
	}

	// 初始化连接（命令行模式下需要）
	if conn == nil {
		conn = newOB11ConnItem()

		// 启动连接服务
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		callback := &ob11Callback{dice: nil}
		conn.SetCallback(callback)

		go conn.Serve(ctx)

		// 等待连接建立
		fmt.Println("正在建立连接...")
		// 简单等待一段时间让连接建立
		// 在实际应用中，应该有更好的方式来检测连接状态
		time.Sleep(3 * time.Second)
	}

	fmt.Printf("即将开始同步 %d 个群组的文件\n", len(config.Groups))

	// 遍历配置中的所有群组进行同步
	successCount := 0
	failedGroups := []string{}

	for _, group := range config.Groups {
		fmt.Printf("开始同步群组: %s (%s)\n", group.ID, group.Alias)

		// 调用内部同步函数
		err := syncSingleGroup(group.ID)
		if err != nil {
			fmt.Printf("同步群组 %s 失败: %v\n", group.ID, err)
			failedGroups = append(failedGroups, group.Alias)
		} else {
			fmt.Printf("同步群组 %s 成功\n", group.Alias)
			successCount++
		}
	}

	// 生成仪表板
	if err := GenerateDashboard(config, fsManager); err != nil {
		zap.S().Warnf("failed to build dashboard: %v", err)
	}

	// 输出同步结果
	fmt.Printf("\n同步全部完成！成功: %d 个群组", successCount)
	if len(failedGroups) > 0 {
		fmt.Printf("，失败: %d 个群组 (%s)", len(failedGroups), strings.Join(failedGroups, ", "))
	}
	fmt.Println()

	basePath := fsManager.GetBasePath()
	fmt.Printf("文件已保存到: %s\n", basePath)

	return nil
}

func newOB11ConnItem() *adapters.PlatformAdapterOB11 {
	// 优先使用配置文件中的设置，如果配置文件中没有设置则使用环境变量
	reverse := config.OneBot11Config.WSReverseURL
	forward := config.OneBot11Config.WSForwardAddr
	accessToken := config.OneBot11Config.AccessToken
	secret := config.OneBot11Config.Secret

	// 如果配置文件中没有设置，则使用环境变量
	if reverse == "" {
		reverse = os.Getenv("OB11_WS_REVERSE")
	}
	if forward == "" {
		forward = os.Getenv("OB11_WS_FORWARD")
	}
	if accessToken == "" {
		accessToken = os.Getenv("OB11_ACCESS_TOKEN")
	}
	if secret == "" {
		secret = os.Getenv("OB11_SECRET")
	}

	// 如果都没有设置，使用默认值
	if reverse == "" && forward == "" {
		reverse = "ws://127.0.0.1:8100/onebot/v11/ws"
	}

	return &adapters.PlatformAdapterOB11{
		WSReverseURL:  reverse,
		WSForwardAddr: forward,
		AccessToken:   accessToken,
		Secret:        secret,
	}
}

func main() {
	fmt.Println("Small Seal (OB11) v0.0.1")

	// 解析命令行参数
	interactive := flag.Bool("i", false, "进入交互等待模式")
	syncAll := flag.Bool("a", false, "同步所有配置的群组文件")
	syncAllLong := flag.Bool("all", false, "同步所有配置的群组文件")
	flag.Parse()

	// 读取配置文件
	config = ReadConfig()

	// 初始化日志
	var logger *zap.Logger
	var err error
	if config.LogFile != "" {
		logConfig := zap.NewProductionConfig()
		logConfig.OutputPaths = []string{config.LogFile}
		logger, err = logConfig.Build()
		if err != nil {
			log.Fatalf("初始化日志失败: %v", err)
		}
	} else {
		logger, _ = zap.NewDevelopment()
	}
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	// 初始化文件系统管理器
	fsManager, err = NewFileSystemManager(config.FileSystemConfig)
	if err != nil {
		log.Fatalf("初始化文件系统管理器失败: %v", err)
	}

	// 检查是否需要执行同步所有群组
	if *syncAll || *syncAllLong {
		fmt.Println("开始执行同步所有群组...")
		if err := syncAllGroupsCLI(); err != nil {
			log.Fatalf("同步失败: %v", err)
		}
		return
	}

	// 只有在交互模式下才启动连接和等待
	if *interactive {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		// d := dice.NewDice()
		conn = newOB11ConnItem()

		callback := &ob11Callback{dice: nil}
		conn.SetCallback(callback)

		go conn.Serve(ctx)

		fmt.Println("小海豹QQ群文件同步器")
		fmt.Println("可用指令:")
		fmt.Println(".同步当前")
		fmt.Println("    同步当前群的群文件，完成后自动生成展示页面")
		fmt.Println(".同步文件 QQ-Group:群号")
		fmt.Println("    指定一个群进行同步，账号应该在群内")
		fmt.Println(".同步全部")
		fmt.Println("    同步 config.yaml 中设置的所有群")
		fmt.Println(".展示页面")
		fmt.Println("    强制重新生成展示页面")
		fmt.Println("等待消息中... 使用 Ctrl+C 退出")

		<-ctx.Done()
		conn.Close()
	} else {
		fmt.Println("程序已启动完成。")
		fmt.Println("使用 -i 参数进入交互等待模式。")
		fmt.Println("使用 -a 或 -all 参数同步所有配置的群组文件。")
	}
}
