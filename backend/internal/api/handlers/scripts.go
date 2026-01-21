package handlers

import _ "embed"

//go:embed scripts/node_exporter_install.sh
var NodeExporterInstallScript string

//go:embed scripts/node_exporter_check_installed.sh
var NodeExporterCheckInstalledScript string

//go:embed scripts/node_exporter_check_version.sh
var NodeExporterCheckVersionScript string

//go:embed scripts/dependencies_install.sh.tmpl
var ServerDependenciesInstallScript string

//go:embed scripts/agent_install.sh.tmpl
var ServerAgentInstallScript string

//go:embed scripts/dependencies_check.sh.tmpl
var ServerDependenciesCheckScript string

//go:embed scripts/deploy_release.sh.tmpl
var ServerReleaseDeployScript string

//go:embed scripts/start_hytale.sh.tmpl
var StartHytaleScript string

//go:embed scripts/node_exporter_check_running.sh
var NodeExporterCheckRunningScript string

//go:embed scripts/node_exporter_check_enabled.sh
var NodeExporterCheckEnabledScript string
