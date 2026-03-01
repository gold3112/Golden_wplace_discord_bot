# Golden Wplace Discord Bot

ユーザーが自分のWplace作品を監視できるセルフサービス型Discord Bot。

## 機能

- **1ユーザー1監視**: 各ユーザーは1つの監視チャンネルを作成可能
- **監視タイプ**:
  - **Progress Monitor**: 作品の完成度追跡（差分減少 = 進捗）
  - **Vandal Monitor**: 荒らし検知（差分増加 = アラート）
- **5分間隔監視**: API負荷を分散
- **自動通知**: 差分検知時にチャンネルへ通知

## セットアップ

### 環境変数

ローカル開発では `.env` を使っても構いませんが、本番・Docker 実行時は **リポジトリ外の secrets ディレクトリ** に env ファイルを置いてください。

```
F:\Dev\VSC\go\secrets\golden_wplace_bot.env
└─ (このリポジトリ)
```

1. `cp .env.example ../secrets/golden_wplace_bot.env`
2. `../secrets/golden_wplace_bot.env` を編集して `DISCORD_TOKEN` 等を設定

ローカルで直接起動する場合は、ルートに `.env` を作成しても OK です。

### 実行（ローカル）

```bash
go run ./cmd/bot
```

### 実行（Docker / docker compose）

```bash
# ../secrets/golden_wplace_bot.env を用意済みであること
docker compose up --build -d
```

- `docker compose` は `docker-compose.yml` が置かれたディレクトリで実行してください
- `./data` が `/app/data` にマウントされ、監視設定やテンプレート画像がコンテナ再起動後も保持されます

## コマンド

### ユーザーコマンド
- `/watch create` - 新規監視チャンネル作成（セットアップ開始）
- `/watch status` - 自分の監視状態確認
- `/watch pause` - 監視一時停止
- `/watch resume` - 監視再開
- `/watch delete` - 監視チャンネル削除

### 管理者コマンド
- `/watch admin list` - サーバー全体の監視一覧
- `/watch admin cleanup` - 非アクティブ監視の削除

## アーキテクチャ

```
cmd/bot/main.go              エントリーポイント
internal/
  ├── watchmanager/          監視エンジン
  ├── setup/                 セットアップウィザード
  ├── notifications/         通知システム
  ├── storage/              データ永続化
  ├── commands/             コマンド実装
  ├── embeds/               Embed生成
  ├── wplace/               Wplace API
  ├── utils/                ユーティリティ
  ├── config/               設定管理
  └── models/               データモデル
```

## データ構造

```
data/guilds/{guild_id}/
  ├── watches.json           監視設定
  ├── template_img/          テンプレート画像
  └── snapshots/             差分スナップショット
```

## 制限事項

- 1ユーザーあたり1監視まで
- 監視間隔は5分固定
- テンプレート画像は5MBまで
