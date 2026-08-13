# ADR 索引

<!-- generate-adr-index.zsh による自動生成。手で編集しない -->

| ADR | タイトル | status | date | 要旨 |
|---|---|---|---|---|
| [0001](0001-implementation-language-zsh.md) | routine runner 本体の実装言語に zsh を選ぶ | accepted | 2026-08-13 | FW 本体を Go ではなく zsh で実装する判断と、その代償（flock 不在・タイムアウトの自前実装） |
| [0002](0002-separate-action-and-info-channels.md) | 要対応と情報を別チャネルに分け、要対応に安定 id を持たせる | accepted | 2026-08-13 | 末尾サマリを要対応・情報の 2 枠に分ける理由と、連続掲示日数を数えるための id 設計 |
