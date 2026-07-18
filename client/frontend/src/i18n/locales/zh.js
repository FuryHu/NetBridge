// 简体中文。
// 主 UI 文案。日志面板的消息保持中文（见 README 国际化说明：后端 Go 日志同样流入该面板，
// 整体保持中文以避免中英混排；如需翻译前端日志可在此扩充）。
export default {
  // 连接服务器页
  'connect.title': '连接服务器',
  'connect.placeholder': '服务器地址，例如 1.2.3.4:10555',
  'connect.button': '连接',
  'connect.connecting': '连接中…',
  'connect.failed': '连接失败：',

  // 加入房间页
  'join.title': '加入房间',
  'join.roomPlaceholder': '房间号',
  'join.namePlaceholder': '昵称',
  'join.button': '加入',

  // 语音
  'voice.on': '语音开',
  'voice.off': '语音关',
  'voice.enabledTip': '语音已启用（点击关闭）',
  'voice.disabledTip': '语音已关闭（点击开启）',
  'voice.micOn': '开麦',
  'voice.micOff': '静音',
  'voice.micOnTip': '关闭麦克风 (F2)',
  'voice.micOffTip': '开启麦克风 (F2)',
  'voice.muteToggleTip': '静音/取消',
  'voice.mutedForYou': '已为你静音',
  'voice.speaking': '正在说话',

  // 顶栏
  'topbar.log': '日志',
  'topbar.leaveRoom': '退出房间',
  'topbar.disconnect': '断开',
  'topbar.vipTip': '虚拟 IP',
  'topbar.tunTip': '虚拟网卡已激活',
  'topbar.ipv6Tip': '服务端可经 IPv6 联系到本机',
  'topbar.ipv4Tip': '仅 IPv4 公网端点',

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

  // 聊天
  'chat.placeholder': '输入消息...',
  'chat.send': '发送',
  'chat.empty': '暂无消息',

  // 日志面板空状态（UI 标签，非日志内容）
  'log.empty': '暂无日志',
  'log.close': '关闭',

  // 确认弹窗
  'modal.cancel': '取消',
  'modal.confirm': '确定',
  'modal.confirmDisconnect': '确定要断开连接吗？',
  'modal.confirmLeaveRoom': '确定要退出房间吗？',

  // 语言切换
  'lang.label': '语言',
}
