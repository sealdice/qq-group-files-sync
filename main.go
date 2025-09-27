package main

// 一个用于调试 OneBot11 适配器的简单入口
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

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
	jsonInfo, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("OnMessageReceived: marshal err, info=%v, err=%v\n", info, err)
		return
	}
	fmt.Printf("OnMessageReceived: %s \n", string(jsonInfo))

	if strings.Contains(info.Message.Segments.ToText(), "群名片") {
		GroupCardNameSetRequest := &adapters.GroupOperationCardNameSetRequest{
			GroupID: info.Message.GroupID,
			UserID:  info.Message.Sender.UserID,
			Name:    info.Message.Segments.ToText(),
		}
		ok, err := conn.GroupCardNameSet(GroupCardNameSetRequest)
		fmt.Println("群名片设置", err, ok, GroupCardNameSetRequest)
	}

	if strings.EqualFold(info.Message.Segments.ToText(), "群信息") {
		_, err := conn.GroupInfoGet(info.Message.GroupID)
		if err != nil {
			fmt.Printf("OnMessageReceived: GroupInfoGet err, GroupID=%s, err=%v\n", info.Message.GroupID, err)
			return
		}
		// fmt.Println("???!", GroupInfoGetResponse)
	}

	if strings.EqualFold(info.Message.Segments.ToText(), "群文件") {
		// 判断发送者不是自己，是自己就退出
		// 坏了 好像不知道自己是谁
		// 似乎如果 sender 是空的 就是自己
		if info.Message.Sender.UserID == "" {
			return
		}

		fileListRequest := &adapters.GroupFileListRequest{
			GroupID: info.Message.GroupID,
		}
		fileListResponse, err := conn.GroupFileList(fileListRequest)
		if err != nil {
			fmt.Printf("OnMessageReceived: GroupFileList err, fileListRequest=%v, err=%v\n", fileListRequest, err)
			return
		}

		fileListResponseJson, err := json.Marshal(fileListResponse)
		if err != nil {
			fmt.Printf("OnMessageReceived: GroupFileList marshal err, fileListResponse=%v, err=%v\n", fileListResponse, err)
			return
		}

		fmt.Println("?!", "群文件列表: "+string(fileListResponseJson))
		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: "群文件列表: " + string(fileListResponseJson)[:1000]},
			},
		})
	}

	if strings.EqualFold(info.Message.Segments.ToText(), "同步文件") {
		// 判断发送者不是自己，是自己就退出
		// 如果 sender 是空的 就是自己
		if info.Message.Sender.UserID == "" {
			return
		}

		// 创建群文件助手，使用配置的文件系统管理器
		helper := NewGroupFileHelper(conn, fsManager)

		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("即将开始同步")},
			},
		})

		// 同步群文件
		err := helper.SyncGroupFiles(info.Message.GroupID)
		if err != nil {
			fmt.Printf("同步群文件失败: %v\n", err)
			conn.MsgSendToGroup(&adapters.MessageSendRequest{
				TargetId: info.Message.GroupID,
				Segments: []types.IMessageElement{
					&types.TextElement{Content: fmt.Sprintf("同步群文件失败: %v", err)},
				},
			})
			return
		}
		if err := GenerateDashboard(config, fsManager); err != nil {
			zap.S().Warnf("failed to build dashboard: %v", err)
		}

		basePath := fsManager.GetBasePath()
		fmt.Printf("群文件同步完成，保存到: %s\n", basePath)
		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("群文件同步完成，已保存到: %s", basePath)},
			},
		})
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

		// 创建群文件助手，使用配置的文件系统管理器
		helper := NewGroupFileHelper(conn, fsManager)

		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("即将开始同步群 %s 的文件", targetGroupID)},
			},
		})

		// 同步指定群的群文件
		err := helper.SyncGroupFiles(targetGroupID)
		if err != nil {
			fmt.Printf("同步群文件失败: %v\n", err)
			conn.MsgSendToGroup(&adapters.MessageSendRequest{
				TargetId: info.Message.GroupID,
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
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("群 %s 文件同步完成，已保存到: %s", targetGroupID, basePath)},
			},
		})
	}

	if strings.EqualFold(info.Message.Segments.ToText(), "展示页面") {
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
	if err := GenerateDashboard(config, fsManager); err != nil {
		zap.S().Warnf("failed to build dashboard: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// d := dice.NewDice()
	conn = newOB11ConnItem()

	callback := &ob11Callback{dice: nil}
	conn.SetCallback(callback)

	go conn.Serve(ctx)

	// d.CallbackForSendMsg.Store("ob11", func(msg *types.MsgToReply) {
	// 	if msg == nil {
	// 		return
	// 	}

	// 	if msg.MessageType == "private" {
	// 		_, _ = conn.MsgSendToPerson(&adapters.MessageSendRequest{
	// 			TargetId: msg.SendTo.UserId,
	// 			Segments: msg.Segments,
	// 		})
	// 	}

	// 	if msg.MessageType == "group" {
	// 		_, _ = conn.MsgSendToGroup(&adapters.MessageSendRequest{
	// 			TargetId: msg.SendTo.GroupId,
	// 			Segments: msg.Segments,
	// 		})
	// 	}
	// })

	fmt.Println("等待消息中... 使用 Ctrl+C 退出")

	<-ctx.Done()
	conn.Close()
}
