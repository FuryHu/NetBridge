// 简体中文。
// 主 UI 文案。日志面板的消息保持中文（见 README 国际化说明：后端 Go 日志同样流入该面板，
// 整体保持中文以避免中英混排；如需翻译前端日志可在此扩充）。
export default {
  // 合并加入表单：服务器 + 房间/邀请 + 昵称。
  // connect.* 复用为服务器栏占位 / 连接态 / 连接错误文案（旧 connect.title/button 已随两步表单移除）。
  'connect.placeholder': '服务器地址，例如 1.2.3.4:10555',
  'connect.connecting': '连接中…',
  'connect.failed': '连接失败：',

  // 加入房间
  'join.serverLabel': '服务器',
  'join.roomLabel': '房间',
  'join.roomOrInvitePlaceholder': '房间号 / 粘贴邀请链接',
  'join.nameLabel': '昵称',
  'join.namePlaceholder': '输入您的昵称',
  'join.button': '加入',
  'join.test': '测试',
  'join.testOk': '测试成功 · {ms} 毫秒',
  'join.testFail': '测试失败',

  // 邀请
  'invite.parsed': '已识别邀请，房间 {room}',
  'invite.detected': '检测到邀请：{room} @ {server}',
  'invite.join': '加入',
  'invite.ignore': '忽略',

  // 语音
  'voice.on': '语音开',
  'voice.off': '语音关',
  'voice.enableLabel': '启用语音',
  'voice.enabledTip': '语音已启用（点击关闭）',
  'voice.disabledTip': '语音已关闭（点击开启）',
  'voice.micOn': '开麦',
  'voice.micOff': '静音',
  'voice.micOnTip': '关闭麦克风 (F2)',
  'voice.micOffTip': '开启麦克风 (F2)',
  'voice.muteToggleTip': '静音/取消',
  'voice.mutedForYou': '已为你静音',
  'voice.speaking': '正在说话',

  // 投屏
  'screen.share': '投屏',
  'screen.sharing': '投屏中',
  'screen.shareTip': '共享屏幕（仅 P2P 直连成员可观看）',
  'screen.stopped': '已停止投屏',
  'screen.started': '已开始投屏',
  'screen.startFail': '投屏启动失败（WebView2 可能不支持 WebCodecs）',
  'screen.noP2P': '当前无 P2P 直连成员，暂无观众',
  'screen.theater': '窗口内全屏',
  'screen.fullscreen': '全屏',
  'screen.bitrate': '码率',
  'screen.quality': '画质',
  'screen.resolution': '分辨率',

  // 顶栏
  'topbar.log': '日志',
  'topbar.disconnect': '断开',
  'topbar.vipTip': '虚拟 IP · 点击复制',
  'topbar.copied': '已复制',
  'topbar.roomCopyTip': '点击复制邀请链接',

  // 成员
  'members.title': '成员',
  'members.self': '我',

  // 状态（后端 State.String() 返回的中文 -> key 映射）
  'status.disconnected': '未连接',
  'status.connecting': '连接中',
  'status.connected': '已连接',
  'status.punching': '打洞中',
  'status.p2p': 'P2P直连',
  'status.relay': '服务器中转',

  // peer hover 标题
  'peer.channelP2P': 'P2P 直连',
  'peer.channelRelay': '服务器中转',
  'peer.channelPending': '连接中',
  'peer.candidateV6': '候选 IPv6: ',
  'peer.badgeRelay': '中转',
  'peer.infoVIP': '虚拟 IP',
  'peer.infoChannel': '连接方式',
  'peer.infoVoice': '语音',
  'peer.infoMic': '麦克风',
  'peer.infoLatency': '延迟',
  'peer.infoOn': '已开启',
  'peer.infoOff': '已关闭',
  'peer.copy': '复制',
  'peer.menuVolume': '音量',
  'peer.menuMute': '静音此人',
  'peer.menuUnmute': '取消静音',

  // 聊天
  'chat.title': '聊天',
  'chat.placeholder': '输入消息...',
  'chat.send': '发送',
  'chat.empty': '暂无消息',

  // 日志面板空状态（UI 标签，非日志内容）
  'log.empty': '暂无日志',
  'log.close': '关闭',

  // 确认弹窗
  'modal.cancel': '取消',
  'modal.confirm': '确定',
  'modal.confirmDisconnect': '确定要断开连接吗？将退出当前房间。',

  // 语言切换
  'lang.label': '语言',

  // 托盘右键菜单（文案由前端翻译后推送给后端）
  'tray.show': '显示主窗口',
  'tray.voice': '语音',
  'tray.quit': '退出 NetBridge',
}
