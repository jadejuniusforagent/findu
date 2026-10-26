package pluginhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// APIVersion 是宿主当前实现的插件 API 版本。清单里的 api_version 必须与它
// 完全一致：插件与宿主同仓编译，不做跨版本兼容，升级时同步改两边。
const APIVersion = "1.0"

// Kind 是插件类型，取值与 contracts/plugin-manifest.schema.json 的 kind 枚举一致。
type Kind string

const (
	KindTool             Kind = "tool"
	KindSkill            Kind = "skill"
	KindRole             Kind = "role"
	KindDataset          Kind = "dataset"
	KindReport           Kind = "report"
	KindNotify           Kind = "notify"
	KindUI               Kind = "ui"
	KindStorage          Kind = "storage"
	KindProviderC2       Kind = "provider-c2"
	KindProviderWebshell Kind = "provider-webshell"
	KindProviderTraffic  Kind = "provider-traffic"
	KindRuntime          Kind = "runtime"
)

var validKinds = map[Kind]bool{
	KindTool: true, KindSkill: true, KindRole: true, KindDataset: true,
	KindReport: true, KindNotify: true, KindUI: true, KindStorage: true,
	KindProviderC2: true, KindProviderWebshell: true, KindProviderTraffic: true,
	KindRuntime: true,
}

// Manifest 对应 contracts/plugin-manifest.schema.json。omitempty 只影响输出；
// 解析时用 DisallowUnknownFields 保证「多写字段」也会失败，与 schema 的
// additionalProperties=false 对齐。
type Manifest struct {
	ID           string         `json:"id"`
	Name         string         `json:"name,omitempty"`
	Version      string         `json:"version"`
	Kind         Kind           `json:"kind"`
	APIVersion   string         `json:"api_version"`
	Description  string         `json:"description,omitempty"`
	Requires     *Requires      `json:"requires,omitempty"`
	Provides     *Provides      `json:"provides"`
	OwnsTables   []string       `json:"owns_tables,omitempty"`
	Migrations   *Migrations    `json:"migrations,omitempty"`
	ConfigSchema map[string]any `json:"config_schema,omitempty"`
	Permissions  []string       `json:"permissions,omitempty"`
	Lifecycle    *Lifecycle     `json:"lifecycle,omitempty"`
	Compat       *Compat        `json:"compat,omitempty"`
}

// Requires 是插件的外部依赖。Plugins 里的 id 必须已注册且处于 started。
type Requires struct {
	API      string   `json:"api,omitempty"`
	Plugins  []string `json:"plugins,omitempty"`
	Binaries []string `json:"binaries,omitempty"`
	Env      []string `json:"env,omitempty"`
}

// Provides 是插件对外暴露的能力名。指针类型用于区分「未声明」与「声明为空」：
// schema 要求 provides 必填，空对象 {} 合法。
type Provides struct {
	Tools     []string `json:"tools,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Datasets  []string `json:"datasets,omitempty"`
	Routes    []string `json:"routes,omitempty"`
	UISlots   []string `json:"ui_slots,omitempty"`
	Events    []string `json:"events,omitempty"`
	Providers []string `json:"providers,omitempty"`
}

// Migrations 指向插件自持表的迁移目录。
type Migrations struct {
	Dir     string `json:"dir,omitempty"`
	Dialect string `json:"dialect,omitempty"`
}

// Lifecycle 是可选的生命周期钩子名（脚本/命令），本批只做声明校验，不执行。
type Lifecycle struct {
	Install   string `json:"install,omitempty"`
	Uninstall string `json:"uninstall,omitempty"`
	Health    string `json:"health,omitempty"`
}

// Compat 是兼容性声明。MinCore 供安装前检查，运行时以 api_version 为准。
type Compat struct {
	MinCore  string   `json:"min_core,omitempty"`
	Adapters []string `json:"adapters,omitempty"`
}

var (
	idPattern         = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	versionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	apiVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	tablePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Validate 只校验清单自身（语法/枚举/必填），不查表归属、不查依赖是否存在；
// 后两项需要宿主状态，见 Registry.Register 与 Registry.Start。
func (m Manifest) Validate() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, args...))
	}
	if !idPattern.MatchString(m.ID) {
		return fail("id %q 不匹配 %s", m.ID, idPattern)
	}
	if !versionPattern.MatchString(m.Version) {
		return fail("version %q 不是 x.y.z", m.Version)
	}
	if !validKinds[m.Kind] {
		return fail("kind %q 不在枚举内", m.Kind)
	}
	if !apiVersionPattern.MatchString(m.APIVersion) {
		return fail("api_version %q 不是 x.y", m.APIVersion)
	}
	if m.Provides == nil {
		return fail("缺少必填字段 provides")
	}
	if m.Requires != nil {
		if m.Requires.API != "" && !apiVersionPattern.MatchString(m.Requires.API) {
			return fail("requires.api %q 不是 x.y", m.Requires.API)
		}
		for _, dep := range m.Requires.Plugins {
			if !idPattern.MatchString(dep) {
				return fail("requires.plugins 里的 %q 不是合法插件 id", dep)
			}
			if dep == m.ID {
				return fail("插件不能依赖自己")
			}
		}
	}
	seen := make(map[string]bool, len(m.OwnsTables))
	for _, t := range m.OwnsTables {
		if !tablePattern.MatchString(t) {
			return fail("owns_tables 里的 %q 不是合法表名", t)
		}
		if seen[t] {
			return fail("owns_tables 重复声明 %q", t)
		}
		seen[t] = true
	}
	if m.Migrations != nil {
		switch m.Migrations.Dialect {
		case "", "postgres", "sqlite":
		default:
			return fail("migrations.dialect %q 只支持 postgres/sqlite", m.Migrations.Dialect)
		}
	}
	return nil
}

// ParseManifest 解析并校验清单。未知字段、多余尾随内容都拒绝。
func ParseManifest(data []byte) (Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: 解析失败: %v", ErrInvalidManifest, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%w: JSON 之后还有多余内容", ErrInvalidManifest)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// LoadManifest 从文件读取并解析清单（通常是 plugins/<id>/plugin.json）。
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: 读取 %s: %v", ErrInvalidManifest, path, err)
	}
	return ParseManifest(data)
}
