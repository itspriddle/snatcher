package tui

import "github.com/itspriddle/snatcher/internal/transmission"

// mockTorrents is the fixed dataset the T1 shell renders. T4 replaces it with a
// real poll; until then it exists so focus, marks, and the detail pane have
// something to act on, and so the goldens have a stable subject.
//
// It is deliberately awkward: every status appears at least once, two labels are
// in play, one torrent carries a server error, and one name contains an ANSI
// escape sequence. That last row is the point — server-derived strings are
// untrusted, and the golden proves the list pane sanitizes them rather than
// letting a torrent name repaint the terminal.
func mockTorrents() []transmission.Torrent {
	return []transmission.Torrent{
		{
			ID: 1, Hash: "0123456789abcdef0123456789abcdef01234567",
			Name: "ubuntu-24.04.1-desktop-amd64.iso", Status: transmission.StatusDownloading,
			PercentDone: 0.42, TotalSize: 6203355136, SizeWhenDone: 6203355136,
			LeftUntilDone: 3597946019, RateDownload: 4194304, RateUpload: 131072,
			UploadRatio: 0.12, ETA: 858, PeersConnected: 24, PeersSendingToUs: 18,
			DownloadDir: "/data/downloads", Labels: []string{"linux"}, QueuePosition: 0,
		},
		{
			ID: 2, Hash: "1123456789abcdef0123456789abcdef01234567",
			Name: "debian-12.7.0-amd64-netinst.iso", Status: transmission.StatusSeeding,
			PercentDone: 1, TotalSize: 661651456, SizeWhenDone: 661651456,
			RateUpload: 524288, UploadRatio: 3.41, ETA: -1, PeersConnected: 9,
			PeersGettingFromUs: 4, DownloadDir: "/data/downloads",
			Labels: []string{"linux", "seed"}, QueuePosition: 1, IsFinished: true,
		},
		{
			ID: 3, Hash: "2123456789abcdef0123456789abcdef01234567",
			Name: "archlinux-2026.09.01-x86_64.iso", Status: transmission.StatusStopped,
			PercentDone: 0.87, TotalSize: 1181116006, SizeWhenDone: 1181116006,
			LeftUntilDone: 153545080, ETA: -1, DownloadDir: "/data/downloads",
			Labels: []string{"linux"}, QueuePosition: 2,
		},
		{
			ID: 4, Hash: "3123456789abcdef0123456789abcdef01234567",
			Name: "fedora-workstation-41.iso", Status: transmission.StatusVerifying,
			PercentDone: 0.99, TotalSize: 2254857830, SizeWhenDone: 2254857830,
			LeftUntilDone: 22548578, ETA: -1, DownloadDir: "/data/downloads",
			QueuePosition: 3,
		},
		{
			ID: 5, Hash: "4123456789abcdef0123456789abcdef01234567",
			Name: "nixos-minimal-25.05-x86_64.iso", Status: transmission.StatusQueuedToVerify,
			PercentDone: 1, TotalSize: 1073741824, SizeWhenDone: 1073741824,
			ETA: -1, DownloadDir: "/data/downloads", QueuePosition: 4,
		},
		{
			ID: 6, Hash: "5123456789abcdef0123456789abcdef01234567",
			Name: "openbsd-7.6-amd64-install.iso", Status: transmission.StatusQueuedToDownload,
			TotalSize: 806354944, SizeWhenDone: 806354944, LeftUntilDone: 806354944,
			ETA: -1, DownloadDir: "/data/downloads", QueuePosition: 5,
		},
		{
			ID: 7, Hash: "6123456789abcdef0123456789abcdef01234567",
			Name: "freebsd-14.1-release-amd64-dvd1.iso", Status: transmission.StatusQueuedToSeed,
			PercentDone: 1, TotalSize: 4509715660, SizeWhenDone: 4509715660,
			UploadRatio: 0.88, ETA: -1, DownloadDir: "/data/downloads",
			Labels: []string{"seed"}, QueuePosition: 6, IsFinished: true,
		},
		{
			ID: 8, Hash: "7123456789abcdef0123456789abcdef01234567",
			Name: "alpine-standard-3.21.0-x86_64.iso", Status: transmission.StatusError,
			PercentDone: 0.31, TotalSize: 209715200, SizeWhenDone: 209715200,
			LeftUntilDone: 144703488, ETA: -1, DownloadDir: "/data/downloads",
			Error: "Tracker gave HTTP response code 404 (Not Found)", QueuePosition: 7,
		},
		{
			ID: 9, Hash: "8123456789abcdef0123456789abcdef01234567",
			Name: "void-live-x86_64-20260901.iso", Status: transmission.StatusDownloading,
			PercentDone: 0.05, TotalSize: 728760320, SizeWhenDone: 728760320,
			LeftUntilDone: 692322304, RateDownload: 786432, ETA: 880,
			PeersConnected: 3, PeersSendingToUs: 2, DownloadDir: "/data/iso",
			QueuePosition: 8,
		},
		{
			ID: 10, Hash: "9123456789abcdef0123456789abcdef01234567",
			Name: "gentoo-admincd-amd64-20260815.iso", Status: transmission.StatusSeeding,
			PercentDone: 1, TotalSize: 587202560, SizeWhenDone: 587202560,
			RateUpload: 65536, UploadRatio: 1.04, ETA: -1, PeersConnected: 2,
			PeersGettingFromUs: 1, DownloadDir: "/data/iso", Labels: []string{"seed"},
			QueuePosition: 9, IsFinished: true,
		},
		{
			ID: 11, Hash: "a123456789abcdef0123456789abcdef01234567",
			Name: "slackware64-15.0-install-dvd.iso", Status: transmission.StatusStopped,
			PercentDone: 0.5, TotalSize: 3865470566, SizeWhenDone: 3865470566,
			LeftUntilDone: 1932735283, ETA: -1, DownloadDir: "/data/iso",
			QueuePosition: 10, IsStalled: true,
		},
		{
			ID: 12, Hash: "b123456789abcdef0123456789abcdef01234567",
			Name:   "hostile\x1b[31mname\x1b[0m\x07-with-escapes.iso",
			Status: transmission.StatusDownloading, PercentDone: 0.17,
			TotalSize: 1048576000, SizeWhenDone: 1048576000, LeftUntilDone: 870318080,
			RateDownload: 262144, ETA: 3319, PeersConnected: 1, PeersSendingToUs: 1,
			DownloadDir: "/data/\x1b[1mdownloads", QueuePosition: 11,
		},
	}
}
