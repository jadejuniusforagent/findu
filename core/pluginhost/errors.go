package pluginhost

import "errors"

// 可判定的错误。调用方用 errors.Is 判断，不要比对字符串。
var (
	ErrInvalidManifest = errors.New("pluginhost: 清单不合法")
	ErrDuplicatePlugin = errors.New("pluginhost: 插件 id 重复")
	ErrPluginNotFound  = errors.New("pluginhost: 插件未注册")
	ErrInvalidState    = errors.New("pluginhost: 状态不允许该操作")
	ErrDependency      = errors.New("pluginhost: 依赖未就绪")
	ErrTableOwnership  = errors.New("pluginhost: 表归属越界")
	ErrInUse           = errors.New("pluginhost: 插件正在被依赖")
)
