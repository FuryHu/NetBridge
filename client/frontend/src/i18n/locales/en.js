// English.
// Main UI strings. Log panel messages are intentionally left out (see README
// i18n note: backend Go logs flow into the same panel; keeping it consistent).
export default {
  // Connect page
  'connect.title': 'Connect to Server',
  'connect.placeholder': 'Server address, e.g. 1.2.3.4:10555',
  'connect.button': 'Connect',
  'connect.connecting': 'Connecting…',
  'connect.failed': 'Connection failed: ',

  // Join room page
  'join.title': 'Join Room',
  'join.roomPlaceholder': 'Room ID',
  'join.namePlaceholder': 'Nickname',
  'join.button': 'Join',

  // Voice
  'voice.on': 'Voice On',
  'voice.off': 'Voice Off',
  'voice.enabledTip': 'Voice enabled (click to disable)',
  'voice.disabledTip': 'Voice disabled (click to enable)',
  'voice.micOn': 'Unmute',
  'voice.micOff': 'Mute',
  'voice.micOnTip': 'Turn off microphone (F2)',
  'voice.micOffTip': 'Turn on microphone (F2)',
  'voice.muteToggleTip': 'Mute/Unmute',
  'voice.mutedForYou': 'Muted for you',
  'voice.speaking': 'Speaking',

  // Top bar
  'topbar.log': 'Log',
  'topbar.leaveRoom': 'Leave Room',
  'topbar.disconnect': 'Disconnect',
  'topbar.vipTip': 'Virtual IP',
  'topbar.tunTip': 'Virtual NIC active',
  'topbar.ipv6Tip': 'Reachable via IPv6',
  'topbar.ipv4Tip': 'IPv4 public endpoint only',

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

  // Chat
  'chat.placeholder': 'Type a message...',
  'chat.send': 'Send',
  'chat.empty': 'No messages',

  // Log panel empty state (UI label, not log content)
  'log.empty': 'No logs',
  'log.close': 'Close',

  // Confirm modal
  'modal.cancel': 'Cancel',
  'modal.confirm': 'Confirm',
  'modal.confirmDisconnect': 'Disconnect from the server?',
  'modal.confirmLeaveRoom': 'Leave the room?',

  // Language switch
  'lang.label': 'Language',
}
