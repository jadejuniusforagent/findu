package app

import (
	"finduai/internal/config"
	"finduai/internal/mcp"
	"finduai/internal/vision"

	"go.uber.org/zap"
)

func registerVisionTools(mcpServer *mcp.Server, cfg *config.Config, logger *zap.Logger) {
	vision.RegisterAnalyzeImageTool(mcpServer, cfg, logger)
}
