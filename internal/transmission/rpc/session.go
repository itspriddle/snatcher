package rpc

// ServerInfo is the description of a connected server: the version triple read
// off a session-get response. It is projected from a Session via
// Session.ServerInfo.
type ServerInfo struct {
	// Version is the server's long version string, e.g. "4.0.6 (38c164933e)".
	Version string `json:"version"`

	// RPCVersionSemver is the server's semver-compatible RPC API version, e.g.
	// "6.0.0".
	RPCVersionSemver string `json:"rpc_version_semver"`

	// Protocol is the negotiated wire protocol name ("jsonrpc-2.0" or "bespoke").
	Protocol string `json:"protocol"`
}

// SessionSettings is the flattened snapshot of a server's configurable session
// settings, projected from a Session via Session.Settings. It carries one typed
// field per setting in the snatcher registry, grouped by category (download,
// network, bandwidth, queue), substituting the zero value when the server omits
// a field. The json tags mirror the other session types (ServerInfo, Stats),
// but the stable scripting contract consumers depend on is the per-key {key,
// category, value} shape the settings command emits, not this struct directly.
type SessionSettings struct {
	// Download settings.

	// DownloadDir is the default download directory.
	DownloadDir string `json:"download_dir"`

	// StartAddedTorrents reports whether torrents start automatically when added.
	StartAddedTorrents bool `json:"start_added_torrents"`

	// RenamePartialFiles reports whether incomplete files get a ".part" suffix.
	RenamePartialFiles bool `json:"rename_partial_files"`

	// IncompleteDirEnabled reports whether incomplete torrents use a separate dir.
	IncompleteDirEnabled bool `json:"incomplete_dir_enabled"`

	// IncompleteDir is the directory for incomplete torrents.
	IncompleteDir string `json:"incomplete_dir"`

	// SeedRatioLimited reports whether the global seed-ratio limit is enforced.
	SeedRatioLimited bool `json:"seed_ratio_limited"`

	// SeedRatioLimit is the global seed-ratio limit.
	SeedRatioLimit float64 `json:"seed_ratio_limit"`

	// IdleSeedingLimitEnabled reports whether idle seeds are stopped after a limit.
	IdleSeedingLimitEnabled bool `json:"idle_seeding_limit_enabled"`

	// IdleSeedingLimit is the idle-seeding limit, in minutes.
	IdleSeedingLimit int `json:"idle_seeding_limit"`

	// CacheSizeMiB is the disk cache size, in MiB.
	CacheSizeMiB int `json:"cache_size_mib"`

	// Network settings.

	// PeerPort is the port for incoming peer connections.
	PeerPort int `json:"peer_port"`

	// PeerPortRandomOnStart reports whether the peer port is randomized at start.
	PeerPortRandomOnStart bool `json:"peer_port_random_on_start"`

	// PortForwardingEnabled reports whether UPnP/NAT-PMP port forwarding is on.
	PortForwardingEnabled bool `json:"port_forwarding_enabled"`

	// Encryption is the peer-encryption mode: "allowed", "preferred", or
	// "required".
	Encryption string `json:"encryption"`

	// PeerLimitGlobal is the global limit on connected peers.
	PeerLimitGlobal int `json:"peer_limit_global"`

	// PeerLimitPerTorrent is the per-torrent limit on connected peers.
	PeerLimitPerTorrent int `json:"peer_limit_per_torrent"`

	// PexEnabled reports whether Peer Exchange is enabled.
	PexEnabled bool `json:"pex_enabled"`

	// DHTEnabled reports whether the Distributed Hash Table is enabled.
	DHTEnabled bool `json:"dht_enabled"`

	// LPDEnabled reports whether Local Peer Discovery is enabled.
	LPDEnabled bool `json:"lpd_enabled"`

	// UTPEnabled reports whether uTP (Micro Transport Protocol) is enabled.
	UTPEnabled bool `json:"utp_enabled"`

	// BlocklistEnabled reports whether the peer blocklist is enabled.
	BlocklistEnabled bool `json:"blocklist_enabled"`

	// BlocklistURL is the URL the blocklist is fetched from.
	BlocklistURL string `json:"blocklist_url"`

	// BlocklistSize is the number of rules in the blocklist (read-only).
	BlocklistSize int `json:"blocklist_size"`

	// Bandwidth settings.

	// SpeedLimitDownEnabled reports whether the global download limit is enforced.
	SpeedLimitDownEnabled bool `json:"speed_limit_down_enabled"`

	// SpeedLimitDown is the global download speed limit, in KB/s.
	SpeedLimitDown int64 `json:"speed_limit_down"`

	// SpeedLimitUpEnabled reports whether the global upload limit is enforced.
	SpeedLimitUpEnabled bool `json:"speed_limit_up_enabled"`

	// SpeedLimitUp is the global upload speed limit, in KB/s.
	SpeedLimitUp int64 `json:"speed_limit_up"`

	// AltSpeedEnabled reports whether the alternate speed limits are active.
	AltSpeedEnabled bool `json:"alt_speed_enabled"`

	// AltSpeedDown is the alternate download speed limit, in KB/s.
	AltSpeedDown int64 `json:"alt_speed_down"`

	// AltSpeedUp is the alternate upload speed limit, in KB/s.
	AltSpeedUp int64 `json:"alt_speed_up"`

	// AltSpeedTimeEnabled reports whether scheduled alt-speed times are used.
	AltSpeedTimeEnabled bool `json:"alt_speed_time_enabled"`

	// AltSpeedTimeBegin is when alt speeds turn on (minutes after midnight).
	AltSpeedTimeBegin int `json:"alt_speed_time_begin"`

	// AltSpeedTimeEnd is when alt speeds turn off (minutes after midnight).
	AltSpeedTimeEnd int `json:"alt_speed_time_end"`

	// AltSpeedTimeDay is the day bitmask for the alt-speed schedule.
	AltSpeedTimeDay int `json:"alt_speed_time_day"`

	// Queue settings.

	// DownloadQueueEnabled reports whether the download queue is enabled.
	DownloadQueueEnabled bool `json:"download_queue_enabled"`

	// DownloadQueueSize is the maximum number of simultaneously downloading torrents.
	DownloadQueueSize int `json:"download_queue_size"`

	// SeedQueueEnabled reports whether the seed queue is enabled.
	SeedQueueEnabled bool `json:"seed_queue_enabled"`

	// SeedQueueSize is the maximum number of simultaneously seeding torrents.
	SeedQueueSize int `json:"seed_queue_size"`

	// QueueStalledEnabled reports whether stalled torrents are ignored by the queue.
	QueueStalledEnabled bool `json:"queue_stalled_enabled"`

	// QueueStalledMinutes is the idle time before a torrent counts as stalled.
	QueueStalledMinutes int `json:"queue_stalled_minutes"`
}

// ServerInfo projects a Session's version triple into a ServerInfo.
func (s Session) ServerInfo() ServerInfo {
	return ServerInfo{
		Version:          s.Version,
		RPCVersionSemver: s.RPCVersionSemver,
		Protocol:         s.Protocol,
	}
}

// Settings flattens a Session's optional settings fields into a SessionSettings,
// substituting the zero value wherever the server omitted a field.
func (s Session) Settings() SessionSettings {
	return SessionSettings{
		DownloadDir:             deref(s.DownloadDir),
		StartAddedTorrents:      deref(s.StartAddedTorrents),
		RenamePartialFiles:      deref(s.RenamePartialFiles),
		IncompleteDirEnabled:    deref(s.IncompleteDirEnabled),
		IncompleteDir:           deref(s.IncompleteDir),
		SeedRatioLimited:        deref(s.SeedRatioLimited),
		SeedRatioLimit:          deref(s.SeedRatioLimit),
		IdleSeedingLimitEnabled: deref(s.IdleSeedingLimitEnabled),
		IdleSeedingLimit:        deref(s.IdleSeedingLimit),
		CacheSizeMiB:            deref(s.CacheSizeMiB),

		PeerPort:              deref(s.PeerPort),
		PeerPortRandomOnStart: deref(s.PeerPortRandomOnStart),
		PortForwardingEnabled: deref(s.PortForwardingEnabled),
		Encryption:            deref(s.Encryption),
		PeerLimitGlobal:       deref(s.PeerLimitGlobal),
		PeerLimitPerTorrent:   deref(s.PeerLimitPerTorrent),
		PexEnabled:            deref(s.PexEnabled),
		DHTEnabled:            deref(s.DHTEnabled),
		LPDEnabled:            deref(s.LPDEnabled),
		UTPEnabled:            deref(s.UTPEnabled),
		BlocklistEnabled:      deref(s.BlocklistEnabled),
		BlocklistURL:          deref(s.BlocklistURL),
		BlocklistSize:         deref(s.BlocklistSize),

		SpeedLimitDownEnabled: deref(s.SpeedLimitDownEnabled),
		SpeedLimitDown:        deref(s.SpeedLimitDown),
		SpeedLimitUpEnabled:   deref(s.SpeedLimitUpEnabled),
		SpeedLimitUp:          deref(s.SpeedLimitUp),
		AltSpeedEnabled:       deref(s.AltSpeedEnabled),
		AltSpeedDown:          deref(s.AltSpeedDown),
		AltSpeedUp:            deref(s.AltSpeedUp),
		AltSpeedTimeEnabled:   deref(s.AltSpeedTimeEnabled),
		AltSpeedTimeBegin:     deref(s.AltSpeedTimeBegin),
		AltSpeedTimeEnd:       deref(s.AltSpeedTimeEnd),
		AltSpeedTimeDay:       deref(s.AltSpeedTimeDay),

		DownloadQueueEnabled: deref(s.DownloadQueueEnabled),
		DownloadQueueSize:    deref(s.DownloadQueueSize),
		SeedQueueEnabled:     deref(s.SeedQueueEnabled),
		SeedQueueSize:        deref(s.SeedQueueSize),
		QueueStalledEnabled:  deref(s.QueueStalledEnabled),
		QueueStalledMinutes:  deref(s.QueueStalledMinutes),
	}
}

// deref returns the value pointed to by p, or the zero value of T when p is nil.
// It flattens the Session's pointer settings fields into SessionSettings' plain
// typed fields.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}

	return *p
}
