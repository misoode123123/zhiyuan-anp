package notif

import (
	"context"
)

// 全局 emitter（main 装配时 Set，各模块通过 Emit 调用，避免 import 循环）。
var globalStore *Store

// SetStore 注入全局 Store（main 调一次）。
func SetStore(s *Store) { globalStore = s }

// Emit 便捷发送通知（nil store 时静默跳过）。
func Emit(userID, psID, ntype, title, message, link string) {
	if globalStore == nil {
		return
	}
	uid := userID
	pid := psID
	_ = globalStore.Create(context.Background(), &Notification{
		UserID:         &uid,
		ProjectSpaceID: &pid,
		Type:           ntype,
		Title:          title,
		Message:        message,
		Link:           link,
	})
}

// EmitBroadcast 广播通知（全员可见）。
func EmitBroadcast(ntype, title, message, link string) {
	if globalStore == nil {
		return
	}
	_ = globalStore.Create(context.Background(), &Notification{
		Type:    ntype,
		Title:   title,
		Message: message,
		Link:    link,
	})
}
