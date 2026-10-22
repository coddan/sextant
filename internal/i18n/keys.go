package i18n

// Key is a typed translation key. Using constants instead of raw strings keeps
// the frontends honest: a typo is a compile error, not a runtime "app.save".
type Key string

const (
	AppName    Key = "app.name"
	AppTagline Key = "app.tagline"

	// Navigation.
	NavDashboard Key = "nav.dashboard"
	NavHosts     Key = "nav.hosts"
	NavTerminal  Key = "nav.terminal"
	NavSFTP      Key = "nav.sftp"
	NavSettings  Key = "nav.settings"
	NavAbout     Key = "nav.about"

	// Common actions and words.
	CommonSave     Key = "common.save"
	CommonCancel   Key = "common.cancel"
	CommonDelete   Key = "common.delete"
	CommonClose    Key = "common.close"
	CommonRetry    Key = "common.retry"
	CommonRefresh  Key = "common.refresh"
	CommonFilter   Key = "common.filter"
	CommonNo       Key = "common.no"
	CommonOK       Key = "common.ok"
	CommonApply    Key = "common.apply"
	CommonOn       Key = "common.on"
	CommonOff      Key = "common.off"
	CommonNone     Key = "common.none"
	CommonLoading  Key = "common.loading"
	CommonEmpty    Key = "common.empty"
	CommonUnknown  Key = "common.unknown"
	CommonRequired Key = "common.required"
	CommonName     Key = "common.name"
	CommonStatus   Key = "common.status"
	CommonDownload Key = "common.download"
	CommonUpload   Key = "common.upload"
	CommonSize     Key = "common.size"

	// Dashboard.
	DashboardEmpty             Key = "dashboard.empty"
	DashboardSearchPlaceholder Key = "dashboard.search_placeholder"
	DashboardAddHost           Key = "dashboard.add_host"
	DashboardImportSSHConfig   Key = "dashboard.import_ssh_config"
	DashboardImportPuTTY       Key = "dashboard.import_putty"
	DashboardShowOnlyOnline    Key = "dashboard.show_only_online"
	DashboardOnline            Key = "dashboard.online"
	DashboardOffline           Key = "dashboard.offline"

	// Host editor.
	HostTitle           Key = "host.title"
	HostNew             Key = "host.new"
	HostEdit            Key = "host.edit"
	HostName            Key = "host.name"
	HostHostname        Key = "host.hostname"
	HostUser            Key = "host.user"
	HostPort            Key = "host.port"
	HostGroup           Key = "host.group"
	HostNotes           Key = "host.notes"
	HostIdentityFiles   Key = "host.identity_files"
	HostCertificateFile Key = "host.certificate_file"
	HostHidden          Key = "host.hidden"
	HostIdentitiesOnly  Key = "host.identities_only"
	HostPassword        Key = "host.password"
	HostPassphrase      Key = "host.passphrase"
	HostSavePassword    Key = "host.save_password"
	HostProxyJump       Key = "host.proxy_jump"
	HostLocalForwards   Key = "host.local_forwards"
	HostDynamicForward  Key = "host.dynamic_forward"
	HostTunnelAutostart Key = "host.tunnel_autostart"
	HostForwardAgent    Key = "host.forward_agent"
	HostMonitoring      Key = "host.monitoring"
	HostDeleteConfirm   Key = "host.delete_confirm"

	// Terminal.
	TerminalFontFamily         Key = "terminal.font_family"
	TerminalFontSize           Key = "terminal.font_size"
	TerminalFind               Key = "terminal.find"
	TerminalFindPlaceholder    Key = "terminal.find_placeholder"
	TerminalSudoAutofill       Key = "terminal.sudo_autofill"
	TerminalStatusConnected    Key = "terminal.status_connected"
	TerminalStatusDisconnected Key = "terminal.status_disconnected"
	TerminalStatusConnecting   Key = "terminal.status_connecting"

	// SFTP.
	SFTPTitle           Key = "sftp.title"
	SFTPRemotePath      Key = "sftp.remote_path"
	SFTPName            Key = "sftp.name"
	SFTPModified        Key = "sftp.modified"
	SFTPPermissions     Key = "sftp.permissions"
	SFTPNewFolder       Key = "sftp.new_folder"
	SFTPDeleteConfirm   Key = "sftp.delete_confirm"
	SFTPDisconnect      Key = "sftp.disconnect"
	SFTPEdit            Key = "sftp.edit"
	SFTPTransferring    Key = "sftp.transferring"
	SFTPTransferDone    Key = "sftp.transfer_done"
	SFTPUploadFile      Key = "sftp.upload_file"
	SFTPDownloadFile    Key = "sftp.download_file"
	SFTPParentDirectory Key = "sftp.parent_directory"
	SFTPEmptyDirectory  Key = "sftp.empty_directory"
	SFTPDragDrop        Key = "sftp.drag_drop"
	SFTPNoSession       Key = "sftp.no_session"

	// Snippets.
	SnippetsTitle     Key = "snippets.title"
	SnippetsNew       Key = "snippets.new"
	SnippetsRun       Key = "snippets.run"
	SnippetsStop      Key = "snippets.stop"
	SnippetsCommand   Key = "snippets.command"
	SnippetsStreaming Key = "snippets.streaming"
	SnippetsOutput    Key = "snippets.output"
	SnippetsEmpty     Key = "snippets.empty"

	// Key setup.
	KeySetupTitle           Key = "keysetup.title"
	KeySetupIntro           Key = "keysetup.intro"
	KeySetupStart           Key = "keysetup.start"
	KeySetupDisablePassword Key = "keysetup.disable_password"
	KeySetupWarning         Key = "keysetup.warning"
	KeySetupRunning         Key = "keysetup.running"
	KeySetupDone            Key = "keysetup.done"
	KeySetupFailed          Key = "keysetup.failed"

	// Forwards.
	ForwardsTitle       Key = "forwards.title"
	ForwardsKindLocal   Key = "forwards.kind_local"
	ForwardsBindAddress Key = "forwards.bind_address"
	ForwardsStop        Key = "forwards.stop"
	ForwardsStart       Key = "forwards.start"

	// Settings.
	SettingsTitle               Key = "settings.title"
	SettingsLanguage            Key = "settings.language"
	SettingsTheme               Key = "settings.theme"
	SettingsThemeDefault        Key = "settings.theme_default"
	SettingsThemeDark           Key = "settings.theme_dark"
	SettingsThemeLight          Key = "settings.theme_light"
	SettingsRefreshInterval     Key = "settings.refresh_interval"
	SettingsMaxConcurrent       Key = "settings.max_concurrent"
	SettingsConfirmCloseTab     Key = "settings.confirm_close_tab"
	SettingsNotificationTimeout Key = "settings.notification_timeout"
	SettingsErrorTimeout        Key = "settings.error_timeout"
	SettingsStickyErrors        Key = "settings.sticky_errors"
	SettingsShowIP              Key = "settings.show_ip"
	SettingsConfigDirectory     Key = "settings.config_directory"
	SettingsSavedNotification   Key = "settings.saved"

	// Notifications.
	NotifyCopied           Key = "notify.copied"
	NotifyConnected        Key = "notify.connected"
	NotifyDisconnected     Key = "notify.disconnected"
	NotifySaved            Key = "notify.saved"
	NotifyDeleted          Key = "notify.deleted"
	NotifyKeySetupComplete Key = "notify.key_setup_complete"
	NotifyImportComplete   Key = "notify.import_complete"
	NotifyImportedCount    Key = "notify.imported_count"

	// Metrics.
	MetricsCPU        Key = "metrics.cpu"
	MetricsRAM        Key = "metrics.ram"
	MetricsDisk       Key = "metrics.disk"
	MetricsCollecting Key = "metrics.collecting"

	// Errors.
	ErrorAuthFailed     Key = "error.auth_failed"
	ErrorHostKeyChanged Key = "error.host_key_changed"
	ErrorTimeout        Key = "error.timeout"
	ErrorInvalidPort    Key = "error.invalid_port"
	ErrorImportFailed   Key = "error.import_failed"

	// Host key prompts.
	HostKeyTitle       Key = "hostkey.title"
	HostKeyUnknownBody Key = "hostkey.unknown_body"
	HostKeyChangedBody Key = "hostkey.changed_body"
	HostKeyFingerprint Key = "hostkey.fingerprint"
	HostKeyAccept      Key = "hostkey.accept"
	HostKeyReject      Key = "hostkey.reject"

	// About.
	AboutWebsite Key = "about.website"
	AboutLicense Key = "about.license"
)
