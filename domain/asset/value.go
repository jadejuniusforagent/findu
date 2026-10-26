package asset

import (
	"errors"
	"fmt"
	"net"
)

// Asset is a row in the assets table.
type Asset struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	CompanyID  *int64  `json:"company_id,omitempty"`
	TaskIDs    []int64 `json:"task_ids"`
	Domain     string  `json:"domain,omitempty"`
	RootDomain string  `json:"root_domain,omitempty"`
	IP         string  `json:"ip,omitempty"`
	CSegment   string  `json:"c_segment,omitempty"`
	Port       *int    `json:"port,omitempty"`
	ICP        string  `json:"icp,omitempty"`
	// ip fields
	BoundDomains []string         `json:"bound_domains,omitempty"`
	OpenPorts    []map[string]any `json:"open_ports,omitempty"`
	// subdomain fields
	RecordType  string   `json:"record_type,omitempty"`
	RecordValue []string `json:"record_value,omitempty"`
	// app fields
	BundleID       string `json:"bundle_id,omitempty"`
	AppName        string `json:"app_name,omitempty"`
	Category       string `json:"category,omitempty"`
	AppDescription string `json:"app_description,omitempty"`
	AppICP         string `json:"app_icp,omitempty"`
	// service fields
	URL           string           `json:"url,omitempty"`
	ServiceType   string           `json:"service_type,omitempty"`
	ServiceName   string           `json:"service_name,omitempty"`
	FaviconMMH3   string           `json:"favicon_mmh3,omitempty"`
	StatusCode    *int             `json:"status_code,omitempty"`
	ContentLength *int64           `json:"content_length,omitempty"`
	PageTitle     string           `json:"page_title,omitempty"`
	Technologies  []string         `json:"technologies,omitempty"`
	Auth          []map[string]any `json:"auth,omitempty"`
	// endpoint fields
	Method string           `json:"method,omitempty"`
	Params []map[string]any `json:"params,omitempty"`
	// findu 共表扩展列（Step 6d R3：PUT/GET assets 全字段回显）
	Status string   `json:"status,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	// meta
	Extra             map[string]any `json:"extra,omitempty"`
	LastSeen          string         `json:"last_seen"`
	TaskSource        string         `json:"task_source,omitempty"` // 装配层 hydrate 填充（任务源信息）
	TaskSourceSummary string         `json:"task_source_summary,omitempty"`
	TaskSourceNodeID  *int64         `json:"task_source_node_id,omitempty"`
}

// AuthItem is one entry in the auth array.
type AuthItem struct {
	Type        string `json:"type,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	Token       string `json:"token,omitempty"`
	Description string `json:"description,omitempty"`
}

// ParamItem is one entry in the params array.
type ParamItem struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Type     string `json:"type,omitempty"`
}

// PortService is one entry in open_ports: {"port":22,"service":"ssh"}.
type PortService struct {
	Port    int    `json:"port"`
	Service string `json:"service,omitempty"`
}

// =====================================================================
// Upsert 请求值类型
// =====================================================================

// UpsertRootDomainReq is the input for UpsertRootDomain.
type UpsertRootDomainReq struct {
	Domain string
	ICP    string
	TaskID int64
}

// UpsertIPReq is the input for UpsertIP.
type UpsertIPReq struct {
	IP           string
	BoundDomains []string
	OpenPorts    []PortService
	TaskID       int64
}

// UpsertSubdomainReq is the input for UpsertSubdomain.
type UpsertSubdomainReq struct {
	Domain      string
	RecordType  string
	RecordValue []string
	ICP         string
	TaskID      int64
}

// UpsertAppReq is the input for UpsertApp.
type UpsertAppReq struct {
	Name        string
	BundleID    string
	Category    string
	Description string
	ICP         string
	CompanyID   *int64 // explicit override; nil = exact ICP auto-attribution when available
	TaskID      int64
}

// UpsertHTTPServiceReq is the input for UpsertHTTPService.
type UpsertHTTPServiceReq struct {
	URL           string
	Technologies  []string
	StatusCode    *int
	ContentLength *int64
	PageTitle     string
	FaviconMMH3   string
	Auth          []map[string]any
	IP            string // optional, from async DNS
	TaskID        int64
}

// UpsertOtherServiceReq is the input for UpsertOtherService.
type UpsertOtherServiceReq struct {
	Domain      string // domain or ip required
	IP          string
	Port        int
	ServiceName string
	Auth        []map[string]any
	TaskID      int64
}

// UpsertEndpointReq is the input for UpsertEndpoint.
type UpsertEndpointReq struct {
	URL    string
	Method string
	Params []map[string]any
	IP     string // optional
	TaskID int64
}

// =====================================================================
// IP 值校验
// =====================================================================

// ErrAssetIPInvalid marks a non-address value in an asset's ip field. Both
// insert_assets and the asset API report it per item with the item index, so one
// bad entry never costs the rest of the batch.
var ErrAssetIPInvalid = errors.New("invalid asset ip")

// ValidateAssetIP keeps hostnames out of assets.ip. Network attribution casts
// that column to inet (see try_inet in schema.sql), so a hostname stored here is
// silently invisible to every IP/CIDR scope rule — the asset simply never gets
// attributed and nobody can tell why. The message states the fix rather than
// just the fault, so an agent can correct the item on its next turn. An empty
// value is accepted: the ip field is optional for service and endpoint assets.
func ValidateAssetIP(value string) error {
	if value == "" || net.ParseIP(value) != nil {
		return nil
	}
	return fmt.Errorf(
		"%w: ip 必须是 IPv4/IPv6 地址，收到 %q。若这是主机名，请改用 type=subdomain 并填 domain 字段；"+
			"若确实要登记地址，请先解析出 A/AAAA 记录，再用解析出的地址填 ip",
		ErrAssetIPInvalid, value)
}
