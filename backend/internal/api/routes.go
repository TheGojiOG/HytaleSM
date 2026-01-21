package api

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yourusername/hytale-server-manager/internal/api/handlers"
	"github.com/yourusername/hytale-server-manager/internal/api/middleware"
	"github.com/yourusername/hytale-server-manager/internal/auth"
	"github.com/yourusername/hytale-server-manager/internal/config"
	"github.com/yourusername/hytale-server-manager/internal/console"
	"github.com/yourusername/hytale-server-manager/internal/database"
	"github.com/yourusername/hytale-server-manager/internal/logging"
	"github.com/yourusername/hytale-server-manager/internal/permissions"
	"github.com/yourusername/hytale-server-manager/internal/server"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
	"github.com/yourusername/hytale-server-manager/internal/websocket"
)

// SetupRouter configures and returns the HTTP router
func SetupRouter(
	cfg *config.Config,
	serverManager *config.ServerManager,
	db *database.DB,
	pool *ssh.ConnectionPool,
	lifecycle *server.LifecycleManager,
	status *server.StatusDetector,
	process server.ProcessManager,
	logger *logging.ActivityLogger,
	hub *websocket.Hub,
	sessionManager *console.SessionManager,
) (*gin.Engine, func()) {
	// Set Gin mode based on environment
	if cfg.Logging.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Global middleware
	router.Use(gin.Recovery())
	router.Use(middleware.Logger())
	router.Use(middleware.Audit(db.DB))
	router.Use(middleware.CORS(cfg.Security.CORS))
	router.Use(middleware.RateLimit(cfg.Security.RateLimit.Enabled, cfg.Security.RateLimit.RequestsPerMinute))
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.ContentSecurityPolicy(cfg.Logging.Level == "debug"))

	// Initialize JWT manager
	jwtManager := auth.NewJWTManager(
		cfg.Auth.JWTSecret,
		parseDuration(cfg.Auth.AccessTokenDuration),
		parseDuration(cfg.Auth.RefreshTokenDuration),
	)

	// Initialize RBAC manager
	rbacManager := auth.NewRBACManager(db.DB)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(db.DB, jwtManager, rbacManager, cfg.Auth.BcryptCost)
	serverHandler := handlers.NewServerHandler(cfg, db, serverManager, rbacManager, pool, lifecycle, status, process, logger, hub)
	userHandler := handlers.NewUserHandler(db.DB, rbacManager, cfg.Auth.BcryptCost)
	backupHandler := handlers.NewBackupHandler(cfg, db.DB, pool)
	consoleHandler := handlers.NewConsoleHandler(cfg, db.DB, hub, sessionManager, pool, rbacManager)
	settingsHandler := handlers.NewSettingsHandler(cfg)
	releaseHandler := handlers.NewReleaseHandler(cfg, db, logger, hub)
	agentHandler := handlers.NewAgentHandler(cfg, db)

	// Public routes
	public := router.Group("/api/v1")
	{
		public.GET("/auth/setup-status", authHandler.SetupStatus)
		public.POST("/auth/setup", authHandler.SetupInitialAdmin)
		public.POST("/auth/register", authHandler.Register)
		public.POST("/auth/login", authHandler.Login)
		public.POST("/auth/refresh", authHandler.RefreshToken)
		public.POST("/agents/cert-issue", agentHandler.IssueCertificate)
		public.GET("/agents/binary", agentHandler.DownloadBinary)
	}

	// Protected routes
	protected := router.Group("/api/v1")
	protected.Use(middleware.Auth(jwtManager))
	{
		// Auth routes
		protected.POST("/auth/logout", authHandler.Logout)
		protected.GET("/auth/me", authHandler.GetCurrentUser)

		// Server routes
		servers := protected.Group("/servers")
		{
			servers.GET("", middleware.RequirePermission(rbacManager, permissions.ServersList), serverHandler.ListServers)
			servers.GET(":id", middleware.RequireServerPermission(rbacManager, permissions.ServersGet), serverHandler.GetServer)
			servers.POST("", middleware.RequirePermission(rbacManager, permissions.ServersCreate), serverHandler.CreateServer)
			servers.PUT(":id", middleware.RequirePermission(rbacManager, permissions.ServersUpdate), serverHandler.UpdateServer)
			servers.DELETE(":id", middleware.RequirePermission(rbacManager, permissions.ServersDelete), serverHandler.DeleteServer)
			servers.POST(":id/test-connection", middleware.RequireServerPermission(rbacManager, permissions.ServersTestConnection), serverHandler.TestConnection)
			servers.GET(":id/metrics", middleware.RequireServerPermission(rbacManager, permissions.ServersMetricsRead), serverHandler.GetMetrics)
			servers.GET(":id/activity", middleware.RequireServerPermission(rbacManager, permissions.ServersActivityRead), serverHandler.GetServerActivity)
			servers.GET(":id/tasks", middleware.RequireServerPermission(rbacManager, permissions.ServersTasksRead), serverHandler.GetServerTasks)
			servers.GET("/metrics/latest", middleware.RequirePermission(rbacManager, permissions.ServersMetricsLatest), serverHandler.GetLatestMetrics)
			servers.GET("/metrics/live", middleware.RequirePermission(rbacManager, permissions.ServersMetricsLive), serverHandler.GetLiveMetrics)
			servers.GET(":id/node-exporter/status", middleware.RequireServerPermission(rbacManager, permissions.ServersNodeExporterStatus), serverHandler.GetNodeExporterStatus)
			servers.POST(":id/node-exporter/install", middleware.RequireServerPermission(rbacManager, permissions.ServersNodeExporterInstall), serverHandler.InstallNodeExporter)

			servers.POST(":id/start", middleware.RequireServerPermission(rbacManager, permissions.ServersStart), serverHandler.StartServer)
			servers.POST(":id/stop", middleware.RequireServerPermission(rbacManager, permissions.ServersStop), serverHandler.StopServer)
			servers.POST(":id/restart", middleware.RequireServerPermission(rbacManager, permissions.ServersRestart), serverHandler.RestartServer)
			servers.GET(":id/status", middleware.RequireServerPermission(rbacManager, permissions.ServersStatusRead), serverHandler.GetServerStatus)
			servers.POST(":id/command", middleware.RequireServerPermission(rbacManager, permissions.ServersConsoleExecute), serverHandler.ExecuteCommand)

			// Backup routes under specific server
			backupHandler.RegisterRoutes(servers, rbacManager)
		}

		// User management routes
		users := protected.Group("/users")
		{
			users.GET("", middleware.RequirePermission(rbacManager, permissions.IAMUsersList), userHandler.ListUsers)
			users.GET(":id", middleware.RequirePermission(rbacManager, permissions.IAMUsersGet), userHandler.GetUser)
			users.POST("", middleware.RequirePermission(rbacManager, permissions.IAMUsersCreate), userHandler.CreateUser)
			users.PUT(":id", middleware.RequirePermission(rbacManager, permissions.IAMUsersUpdate), userHandler.UpdateUser)
			users.DELETE(":id", middleware.RequirePermission(rbacManager, permissions.IAMUsersDelete), userHandler.DeleteUser)
			users.PUT(":id/roles", middleware.RequirePermission(rbacManager, permissions.IAMUsersRolesUpdate), userHandler.AssignRoles)
		}

		// Console routes
		protected.GET("/servers/:id/console/history", middleware.RequireServerPermission(rbacManager, permissions.ServersConsoleHistoryRead), consoleHandler.GetCommandHistory)
		protected.GET("/servers/:id/console/history/search", middleware.RequireServerPermission(rbacManager, permissions.ServersConsoleHistorySearch), consoleHandler.SearchCommandHistory)
		protected.GET("/servers/:id/console/autocomplete", middleware.RequireServerPermission(rbacManager, permissions.ServersConsoleAutocomplete), consoleHandler.GetAutocomplete)
		protected.POST("/servers/:id/dependencies/install", middleware.RequireServerPermission(rbacManager, permissions.ServersDependenciesInstall), serverHandler.InstallDependencies)
		protected.POST("/servers/:id/agent/install", middleware.RequireServerPermission(rbacManager, permissions.ServersAgentInstall), serverHandler.InstallAgent)
		protected.GET("/servers/:id/agent/state", middleware.RequireServerPermission(rbacManager, permissions.ServersAgentStateRead), serverHandler.GetAgentState)
		protected.POST("/servers/:id/processes/kill", middleware.RequireServerPermission(rbacManager, permissions.ServersProcessKill), serverHandler.KillProcess)
		protected.GET("/servers/:id/dependencies/check", middleware.RequireServerPermission(rbacManager, permissions.ServersDependenciesCheck), serverHandler.CheckDependencies)
		protected.POST("/servers/:id/releases/deploy", middleware.RequireServerPermission(rbacManager, permissions.ServersReleaseDeploy), serverHandler.DeployRelease)
		protected.POST("/servers/:id/transfer/benchmark", middleware.RequireServerPermission(rbacManager, permissions.ServersTransferBenchmark), serverHandler.StartTransferBenchmark)

		// Settings routes
		protected.GET("/settings", middleware.RequirePermission(rbacManager, permissions.SettingsGet), settingsHandler.GetSettings)
		protected.PUT("/settings", middleware.RequirePermission(rbacManager, permissions.SettingsUpdate), settingsHandler.UpdateSettings)

		// Releases routes
		releases := protected.Group("/releases")
		{
			releases.GET("", middleware.RequirePermission(rbacManager, permissions.ReleasesList), releaseHandler.ListReleases)
			releases.GET("/:id", middleware.RequirePermission(rbacManager, permissions.ReleasesGet), releaseHandler.GetRelease)
			releases.DELETE("/:id", middleware.RequirePermission(rbacManager, permissions.ReleasesDelete), releaseHandler.DeleteRelease)
			releases.GET("/jobs", middleware.RequirePermission(rbacManager, permissions.ReleasesJobsList), releaseHandler.ListJobs)
			releases.GET("/jobs/:id", middleware.RequirePermission(rbacManager, permissions.ReleasesJobsGet), releaseHandler.GetJob)
			releases.POST("/download", middleware.RequirePermission(rbacManager, permissions.ReleasesDownload), releaseHandler.DownloadRelease)
			releases.POST("/downloader/init", middleware.RequirePermission(rbacManager, permissions.ReleasesDownload), releaseHandler.InitDownloader)
			releases.POST("/print-version", middleware.RequirePermission(rbacManager, permissions.ReleasesPrintVersion), releaseHandler.PrintVersion)
			releases.POST("/check-update", middleware.RequirePermission(rbacManager, permissions.ReleasesCheckUpdate), releaseHandler.CheckUpdate)
			releases.POST("/downloader-version", middleware.RequirePermission(rbacManager, permissions.ReleasesDownloaderVersion), releaseHandler.DownloaderVersion)
			releases.GET("/downloader/status", middleware.RequirePermission(rbacManager, permissions.ReleasesDownloaderVersion), releaseHandler.DownloaderStatus)
			releases.GET("/downloader/auth", middleware.RequirePermission(rbacManager, permissions.ReleasesDownloaderVersion), releaseHandler.DownloaderAuthStatus)
			releases.POST("/reset-auth", middleware.RequirePermission(rbacManager, permissions.ReleasesResetAuth), releaseHandler.ResetDownloaderAuth)
		}

		// IAM routes (roles/permissions)
		iamHandler := handlers.NewIAMHandler(db.DB)
		iam := protected.Group("/iam")
		{
			iam.GET("/permissions", middleware.RequirePermission(rbacManager, permissions.IAMPermissionsList), iamHandler.ListPermissions)
			iam.GET("/roles", middleware.RequirePermission(rbacManager, permissions.IAMRolesList), iamHandler.ListRoles)
			iam.GET("/roles/:id", middleware.RequirePermission(rbacManager, permissions.IAMRolesGet), iamHandler.GetRole)
			iam.POST("/roles", middleware.RequirePermission(rbacManager, permissions.IAMRolesCreate), iamHandler.CreateRole)
			iam.PUT("/roles/:id", middleware.RequirePermission(rbacManager, permissions.IAMRolesUpdate), iamHandler.UpdateRole)
			iam.DELETE("/roles/:id", middleware.RequirePermission(rbacManager, permissions.IAMRolesDelete), iamHandler.DeleteRole)
			iam.PUT("/roles/:id/permissions", middleware.RequirePermission(rbacManager, permissions.IAMRolesPermissionsUpdate), iamHandler.SetRolePermissions)
			iam.GET("/audit-logs", middleware.RequirePermission(rbacManager, permissions.IAMAuditLogsList), iamHandler.ListAuditLogs)
		}

		// WebSocket routes (authentication handled in handler)
		protected.GET("/ws/console/:id", consoleHandler.HandleConsoleWebSocket)
		protected.GET("/ws/servers/:id/tasks", middleware.RequireServerPermission(rbacManager, permissions.ServersTransferBenchmark), serverHandler.HandleServerTasksWebSocket)
		protected.GET("/ws/releases/jobs/:id", middleware.RequirePermission(rbacManager, permissions.ReleasesJobsStream), releaseHandler.HandleReleaseJobWebSocket)
	}

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	shutdown := func() {
		log.Println("Waiting for background server operations to complete...")
		serverHandler.WaitForCompletion()
		log.Println("Background operations completed")
	}

	return router, shutdown
}

// parseDuration is a helper to parse duration strings
// For now, we'll use a simple implementation
func parseDuration(duration string) time.Duration {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return 15 * time.Minute // Default fallback
	}
	return d
}
