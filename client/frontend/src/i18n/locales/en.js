// English.
// Main UI strings. Log panel messages are intentionally left out (see README
// i18n note: backend Go logs flow into the same panel; keeping it consistent).
export default {
  // Merged join form: server + room/invite + nickname.
  // connect.* reused for server field placeholder / connecting / error
  // (old connect.title/button removed with the two-step form).
  'connect.placeholder': 'Server address, e.g. 1.2.3.4:10555',
  'connect.connecting': 'Connecting…',
  'connect.failed': 'Connection failed: ',

  // Join room
  'join.serverLabel': 'Server',
  'join.roomLabel': 'Room',
  'join.roomOrInvitePlaceholder': 'Room ID / paste invite link',
  'join.nameLabel': 'Name',
  'join.namePlaceholder': 'Enter your name',
  'join.button': 'Join',
  'join.test': 'Test',
  'join.testOk': 'Success · {ms} ms',
  'join.testFail': 'Failed',

  // Invite
  'invite.parsed': 'Invite recognized - room {room}',
  'invite.detected': 'Invite detected: {room} @ {server}',
  'invite.join': 'Join',
  'invite.ignore': 'Ignore',

  // Voice
  'voice.on': 'Voice On',
  'voice.off': 'Voice Off',
  'voice.enableLabel': 'Enable voice',
  'voice.enabledTip': 'Voice enabled (click to disable)',
  'voice.disabledTip': 'Voice disabled (click to enable)',
  'voice.micOn': 'Unmute',
  'voice.micOff': 'Mute',
  'voice.micOnTip': 'Turn off microphone (F2)',
  'voice.micOffTip': 'Turn on microphone (F2)',
  'voice.muteToggleTip': 'Mute/Unmute',
  'voice.mutedForYou': 'Muted for you',
  'voice.speaking': 'Speaking',

  // Screen share
  'screen.share': 'Share',
  'screen.sharing': 'Sharing',
  'screen.shareTip': 'Share screen (only P2P peers can view)',
  'screen.stopped': 'Screen share stopped',
  'screen.started': 'Screen share started',
  'screen.startFail': 'Screen share failed (WebView2 may lack WebCodecs)',
  'screen.unsupported': 'Screen share is not supported on this platform',
  'screen.noP2P': 'No P2P peers connected, no viewers',
  'screen.theater': 'Theater mode',
  'screen.fullscreen': 'Fullscreen',
  'screen.bitrate': 'Bitrate',
  'screen.quality': 'Quality',
  'screen.resolution': 'Resolution',

  // Top bar
  'topbar.log': 'Log',
  'topbar.disconnect': 'Disconnect',
  'topbar.vipTip': 'Virtual IP · click to copy',
  'topbar.copied': 'Copied',
  'topbar.roomCopyTip': 'Click to copy invite link',

  // Members
  'members.title': 'Members',
  'members.self': 'Me',

  // Status (backend State.String() Chinese -> key mapping)
  'status.disconnected': 'Disconnected',
  'status.connecting': 'Connecting',
  'status.connected': 'Connected',
  'status.punching': 'Hole punching',
  'status.p2p': 'P2P Direct',
  'status.relay': 'Server Relay',

  // Peer hover title
  'peer.channelP2P': 'P2P direct',
  'peer.channelRelay': 'Server relay',
  'peer.channelPending': 'Connecting',
  'peer.candidateV6': 'Candidate IPv6: ',
  'peer.badgeRelay': 'Relay',
  'peer.infoVIP': 'Virtual IP',
  'peer.infoChannel': 'Connection',
  'peer.infoVoice': 'Voice',
  'peer.infoMic': 'Microphone',
  'peer.infoLatency': 'Latency',
  'peer.infoOn': 'On',
  'peer.infoOff': 'Off',
  'peer.copy': 'Copy',
  'peer.menuVolume': 'Volume',
  'peer.menuMute': 'Mute for me',
  'peer.menuUnmute': 'Unmute',

  // Chat
  'chat.title': 'Chat',
  'chat.placeholder': 'Type a message...',
  'chat.send': 'Send',
  'chat.empty': 'No messages',

  // Log panel empty state (UI label, not log content)
  'log.empty': 'No logs',
  'log.close': 'Close',

  // Confirm modal
  'modal.cancel': 'Cancel',
  'modal.confirm': 'Confirm',
  'modal.confirmDisconnect': 'Disconnect from the server? This will leave the current room.',

  // Language switch
  'lang.label': 'Language',

  // System tray context menu (translated by frontend, pushed to backend)
  'tray.show': 'Show Window',
  'tray.voice': 'Voice',
  'tray.quit': 'Quit NetBridge',
}
