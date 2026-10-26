package server

import (
	"finduai/core/config"
	"finduai/core/log"
	vulnfp "finduai/fingerprint"
	intelplugin "finduai/plugins/intel"
	"finduai/plugins/intel/vulnintel"
	"finduai/plugins/intel/vulnstore"
	"finduai/vulnkb"
)

// startIntelPlugin 把第一个真实插件装配进宿主：注册 + 启动。
//
// 依赖全部用接口/闭包注入，插件本身不 import finduai/db、finduai/server：
//   - Store：漏洞库/情报读写（plugins/intel/vulnstore 实现）；
//   - Bootstrap：指纹规则库 + 1day 漏洞库导入（幂等，首次 Start 触发）；
//   - Intel：vulnintel 抓取/富化管线，插件 Start/Stop 管理其生命周期。
//
// DB 不可用时（部分单测直接构造 Manager）跳过装配，路由不会注册。
func (s *Server) startIntelPlugin() {
	if s == nil || s.m == nil || s.m.pg == nil || s.plugins == nil {
		return
	}
	dir := config.LoadSettings().VulnKBDir
	if dir == "" {
		dir = vulnkb.DefaultDir
	}
	vstore := s.m.vulnStore
	if vstore == nil {
		vstore = vulnstore.New(s.m.pg)
		s.m.pg.SetVulnKBStore(vstore)
	}
	p, err := intelplugin.New(intelplugin.Deps{
		Store: vstore,
		Bootstrap: func() {
			// 指纹规则库：建表 + 24h 自动同步（上游不可达时用内置快照兜底）。
			if fpStore, err := vulnfp.NewStore(s.m.pg.DB); err != nil {
				log.Printf("[fingerprint] 建表失败: %v", err)
			} else {
				vulnfp.NewUpdater(fpStore, func() string {
					v, _, _ := s.m.pg.GetSetting("fingerprint.github_token")
					return v
				}).Start()
			}
			// 1day 漏洞库：按 content_hash 幂等导入，放好 vulnkb/ 目录重启即入库。
			vulnkb.Bootstrap(vstore, dir)
		},
		Intel: vulnintel.NewManager(vstore, s.m.pg),
	})
	if err != nil {
		log.Printf("[plugins] intel 装配失败: %v", err)
		return
	}
	if err := s.plugins.Register(p); err != nil {
		log.Printf("[plugins] intel 注册失败: %v", err)
		return
	}
	if err := s.plugins.Start(s.ctx, p.Manifest().ID); err != nil {
		log.Printf("[plugins] intel 启动失败: %v", err)
	}
}
