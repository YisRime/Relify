package core

import (
	"Relify/internal/model"
	"Relify/internal/storage"
)

// GetRouteStore 获取路由存储（用于 API 访问）
func (c *Core) GetRouteStore() *storage.RouteStore {
	return c.routeStore
}

// GetUserMapStore 获取用户映射存储（用于 API 访问）
func (c *Core) GetUserMapStore() *storage.UserMapStore {
	return c.userMapStore
}

// GetMessageMapStore 获取消息映射存储（用于 API 访问）
func (c *Core) GetMessageMapStore() *storage.MessageMapStore {
	return c.messageMapStore
}

// SaveBinding 保存房间绑定
func (c *Core) SaveBinding(binding *model.RoomBinding) error {
	return c.routeStore.SaveBinding(binding)
}

// GetBinding 获取房间绑定
func (c *Core) GetBinding(id string) (*model.RoomBinding, bool) {
	return c.routeStore.GetBinding(id)
}

// DeleteBinding 删除房间绑定
func (c *Core) DeleteBinding(id string) error {
	return c.routeStore.DeleteBinding(id)
}

// ListBindings 列出所有房间绑定
func (c *Core) ListBindings() []*model.RoomBinding {
	return c.routeStore.ListBindings()
}

// SaveUserMapping 保存用户映射
func (c *Core) SaveUserMapping(mapping *storage.UserMapping) error {
	return c.userMapStore.Save(mapping)
}

// GetUserMapping 获取用户映射
func (c *Core) GetUserMapping(sourceDriver, sourceUserID, targetDriver string) (*storage.UserMapping, bool) {
	return c.userMapStore.GetMapping(sourceDriver, sourceUserID, targetDriver)
}
