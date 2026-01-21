package permissions

import "testing"

func TestPermissionConstants(t *testing.T) {
	constants := []string{
		ServersList,
		ServersGet,
		ServersCreate,
		ServersUpdate,
		ServersDelete,
		ServersTestConnection,
		ServersMetricsRead,
		ServersMetricsLatest,
		ServersMetricsLive,
		ServersActivityRead,
		ServersNodeExporterStatus,
		ServersNodeExporterInstall,
		ServersStart,
		ServersStop,
		ServersRestart,
		ServersStatusRead,
		ServersConsoleView,
		ServersConsoleExecute,
		ServersConsoleHistoryRead,
		ServersConsoleHistorySearch,
		ServersConsoleAutocomplete,
		ServersTasksRead,
		ServersBackupsCreate,
		ServersBackupsList,
		ServersBackupsGet,
		ServersBackupsRestore,
		ServersBackupsDelete,
		ServersBackupsRetentionEnforce,
		SettingsGet,
		SettingsUpdate,
		ReleasesList,
		ReleasesGet,
		ReleasesJobsList,
		ReleasesJobsGet,
		ReleasesJobsStream,
		ReleasesDownload,
		ReleasesPrintVersion,
		ReleasesCheckUpdate,
		ReleasesDownloaderVersion,
		ReleasesResetAuth,
		IAMUsersList,
		IAMUsersGet,
		IAMUsersCreate,
		IAMUsersUpdate,
		IAMUsersDelete,
		IAMUsersRolesUpdate,
		IAMRolesList,
		IAMRolesGet,
		IAMRolesCreate,
		IAMRolesUpdate,
		IAMRolesDelete,
		IAMRolesPermissionsUpdate,
		IAMPermissionsList,
		IAMAuditLogsList,
	}

	seen := map[string]bool{}
	for _, value := range constants {
		if value == "" {
			t.Fatalf("expected non-empty permission constant")
		}
		if seen[value] {
			t.Fatalf("duplicate permission constant: %s", value)
		}
		seen[value] = true
	}
}
