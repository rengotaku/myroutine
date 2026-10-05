# ADR 索引

<!-- generate-adr-index.zsh による自動生成。手で編集しない -->

| ADR | タイトル | status | date | 要旨 |
|---|---|---|---|---|
| [0001](0001-implementation-language-zsh.md) | routine runner 本体の実装言語に zsh を選ぶ | accepted | 2026-08-13 | FW 本体を Go ではなく zsh で実装する判断と、その代償（flock 不在・タイムアウトの自前実装） |
| [0002](0002-separate-action-and-info-channels.md) | 要対応と情報を別チャネルに分け、要対応に安定 id を持たせる | accepted | 2026-08-13 | 末尾サマリを要対応・情報の 2 枠に分ける理由と、連続掲示日数を数えるための id 設計 |
| [0003](0003-cadence-as-skip.md) | 週次・月次の実行間隔を本体の skip として扱う | accepted | 2026-08-19 | step_cadence / step_cadence_weekday を本体側の skip 判定として実装した理由と、ステップ内で日付を見る案・別コマンドに分ける案を捨てた理由 |
| [0004](0004-parallel-as-contiguous-groups.md) | 並列実行を「連続する parallel ステップのグループ」として扱う | accepted | 2026-08-21 | step_parallel を実行順の連続範囲でグループ化し、出力を番号順に flush する形を選んだ理由と、依存グラフ案・全ステップ既定並列案を捨てた理由 |
| [0005](0005-todo-as-third-summary-channel.md) | 定常のやることを第 3 のチャネル (add_todo) に分ける | accepted | 2026-08-28 | 毎日そこにある定常のやることを要対応にも情報にも載せず、履歴を持たない第 3 枠として分けた理由 |
| [0006](0006-runner-in-go-with-zsh-step-prelude.md) | runner を Go で書き直し、ステップは zsh の prelude 越しに実行する | proposed | 2026-10-05 | ADR 0001 で捨てた Go 案を採り直す判断。境界を「環境変数で渡し、fd 3 の行で返す」既存の契約に固定し、プリミティブは zsh の prelude に閉じ込める |
