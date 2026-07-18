// 日本語。
// メインUI文字列。ログパネルのメッセージは除外（READMEのi18n注記参照：
// バックエンドのGoログが同じパネルに流れるため、一貫性を保つ）。
export default {
  // サーバー接続ページ
  'connect.title': 'サーバーに接続',
  'connect.placeholder': 'サーバーアドレス（例: 1.2.3.4:10555）',
  'connect.button': '接続',
  'connect.connecting': '接続中…',
  'connect.failed': '接続失敗: ',

  // ルーム参加ページ
  'join.title': 'ルームに参加',
  'join.roomPlaceholder': 'ルームID',
  'join.namePlaceholder': 'ニックネーム',
  'join.button': '参加',

  // 音声
  'voice.on': '音声オン',
  'voice.off': '音声オフ',
  'voice.enabledTip': '音声有効（クリックで無効化）',
  'voice.disabledTip': '音声無効（クリックで有効化）',
  'voice.micOn': 'ミュート解除',
  'voice.micOff': 'ミュート',
  'voice.micOnTip': 'マイクをオフ (F2)',
  'voice.micOffTip': 'マイクをオン (F2)',
  'voice.muteToggleTip': 'ミュート切替',
  'voice.mutedForYou': 'ミュート済み',
  'voice.speaking': '発話中',

  // トップバー
  'topbar.log': 'ログ',
  'topbar.leaveRoom': '退室',
  'topbar.disconnect': '切断',
  'topbar.vipTip': '仮想IP',
  'topbar.tunTip': '仮想NIC有効',
  'topbar.ipv6Tip': 'IPv6で到達可能',
  'topbar.ipv4Tip': 'IPv4のみ',

  // メンバー
  'members.title': 'メンバー',
  'members.self': '自分',

  // ステータス（バックエンド State.String() の中国語 -> キーへマッピング）
  'status.disconnected': '切断',
  'status.connecting': '接続中',
  'status.connected': '接続済',
  'status.punching': 'NAT越え中',
  'status.p2p': 'P2P直接',
  'status.relay': 'サーバー中継',

  // peer ホバータイトル
  'peer.channelP2P': 'P2P直接',
  'peer.channelRelay': 'サーバー中継',
  'peer.channelPending': '接続中',
  'peer.candidateV6': '候補 IPv6: ',
  'peer.badgeRelay': '中継',

  // チャット
  'chat.placeholder': 'メッセージを入力...',
  'chat.send': '送信',
  'chat.empty': 'メッセージなし',

  // ログパネル空状態（UIラベル、ログ内容ではない）
  'log.empty': 'ログなし',
  'log.close': '閉じる',

  // 確認ダイアログ
  'modal.cancel': 'キャンセル',
  'modal.confirm': '確認',
  'modal.confirmDisconnect': 'サーバーから切断しますか？',
  'modal.confirmLeaveRoom': 'ルームを退室しますか？',

  // 言語切替
  'lang.label': '言語',
}
