package main

// 一个用于调试 OneBot11 适配器的简单入口

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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

var conn *adapters.PlatformAdapterOB11

func (cb *ob11Callback) OnMessageReceived(info *adapters.MessageSendCallbackInfo) {
	jsonInfo, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("OnMessageReceived: marshal err, info=%v, err=%v\n", info, err)
		return
	}
	fmt.Printf("OnMessageReceived: %s, msg=%s\n", string(jsonInfo), info.Message.Segments.ToText())

	if strings.Contains(info.Message.Segments.ToText(), "群名片") {
		GroupCardNameSetRequest := &adapters.GroupOperationCardNameSetRequest{
			GroupID: info.Message.GroupID,
			UserID:  info.Message.Sender.UserID,
			Name:    info.Message.Segments.ToText(),
		}
		ok, err := conn.GroupCardNameSet(GroupCardNameSetRequest)
		fmt.Println("群名片设置", err, ok, GroupCardNameSetRequest)
	}

	if strings.Contains(info.Message.Segments.ToText(), "群信息") {
		GroupInfoGetResponse, err := conn.GroupInfoGet(info.Message.GroupID)
		if err != nil {
			fmt.Printf("OnMessageReceived: GroupInfoGet err, GroupID=%d, err=%v\n", info.Message.GroupID, err)
			return
		}
		fmt.Println("???!", GroupInfoGetResponse)
	}

	if strings.Contains(info.Message.Segments.ToText(), "群文件") {
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

	if strings.Contains(info.Message.Segments.ToText(), "同步文件") {
		// 判断发送者不是自己，是自己就退出
		// 如果 sender 是空的 就是自己
		if info.Message.Sender.UserID == "" {
			return
		}

		// 创建群文件助手，设置下载路径为 data/群号
		// 清理群号中的不安全字符，避免Windows文件名问题
		downloadPath := "./data"
		helper := NewGroupFileHelper(conn, downloadPath)

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

		fmt.Printf("群文件同步完成，保存到: %s\n", downloadPath)
		conn.MsgSendToGroup(&adapters.MessageSendRequest{
			TargetId: info.Message.GroupID,
			Segments: []types.IMessageElement{
				&types.TextElement{Content: fmt.Sprintf("群文件同步完成，已保存到: %s", downloadPath)},
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
	reverse := os.Getenv("OB11_WS_REVERSE")
	forward := os.Getenv("OB11_WS_FORWARD")
	if reverse == "" && forward == "" {
		reverse = "ws://127.0.0.1:8100/onebot/v11/ws"
	}

	return &adapters.PlatformAdapterOB11{
		WSReverseURL:  reverse,
		WSForwardAddr: forward,
		AccessToken:   os.Getenv("OB11_ACCESS_TOKEN"),
		Secret:        os.Getenv("OB11_SECRET"),
	}
}

func main() {
	fmt.Println("Small Seal (OB11) v0.0.1")
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

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
