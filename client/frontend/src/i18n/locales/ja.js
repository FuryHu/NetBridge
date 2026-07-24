// 日本語。
// メインUI文字列。ログパネルのメッセージは除外（READMEのi18n注記参照：
// バックエンドのGoログが同じパネルに流れるため、一貫性を保つ）。
export default {
  // 統合参加フォーム：サーバー + ルーム/招待 + ニックネーム。
  // connect.* はサーバー欄プレースホルダ / 接続中 / エラーに再利用
  // （旧 connect.title/button は二段階フォームと共に削除）。
  'connect.placeholder': 'サーバーアドレス（例: 1.2.3.4:10555）',
  'connect.connecting': '接続中…',
  'connect.failed': '接続失敗: ',

  // ルーム参加
  'join.serverLabel': 'サーバー',
  'join.roomLabel': 'ルーム',
  'join.roomOrInvitePlaceholder': 'ルームID / 招待リンクを貼り付け',
  'join.nameLabel': '名前',
  'join.namePlaceholder': '空欄で自動生成',
  'join.button': '参加',
  'join.test': 'テスト',
  'join.testOk': '成功 · {ms} ms',
  'join.testFail': '失敗',

  // 招待
  'invite.parsed': '招待を認識しました - ルーム {room}',
  'invite.detected': '招待を検出：{room} @ {server}',
  'invite.join': '参加',
  'invite.ignore': '無視',

  // 音声
  'voice.on': '音声オン',
  'voice.off': '音声オフ',
  'voice.enableLabel': '音声を有効化',
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
  'topbar.disconnect': '切断',
  'topbar.vipTip': '仮想IP · クリックでコピー',
  'topbar.copied': 'コピー済み',
  'topbar.roomCopyTip': 'クリックで招待リンクをコピー',

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
  'peer.infoVIP': '仮想 IP',
  'peer.infoChannel': '接続方式',
  'peer.infoVoice': '音声',
  'peer.infoMic': 'マイク',
  'peer.infoLatency': '遅延',
  'peer.infoOn': 'オン',
  'peer.infoOff': 'オフ',
  'peer.copy': 'コピー',
  'peer.menuVolume': '音量',
  'peer.menuMute': 'この人をミュート',
  'peer.menuUnmute': 'ミュート解除',

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
  'modal.confirmDisconnect': 'サーバーから切断しますか？現在のルームを退室します。',

  // 言語切替
  'lang.label': '言語',

  // システムトレイ右クリックメニュー（フロントエンドが翻訳してバックエンドへ送信）
  'tray.show': 'ウィンドウを表示',
  'tray.voice': '音声',
  'tray.quit': '終了',
}
