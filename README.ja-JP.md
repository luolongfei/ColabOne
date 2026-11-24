<div align="center">
  <img src="console/resources/icon.png" width="96" height="96" alt="ColabOne" />
  <h1>ColabOne</h1>
  <p><strong>Google Colab GPU 向けの軽量デスクトップワークスペース — リモート環境で OpenAI 互換の GGUF LLM を公開し、ComfyUI ワークフローを実行できます。</strong></p>

  <p>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-2563eb?style=flat-square" alt="ライセンス: GPL-3.0" /></a>
    <img src="https://img.shields.io/badge/Platform-macOS%20Apple%20Silicon%20%7C%20Windows%20x64-475569?style=flat-square" alt="対応プラットフォーム: macOS Apple Silicon | Windows x64" />
    <img src="https://img.shields.io/badge/OpenAI%20API-Responses%20%26%20Chat-0f766e?style=flat-square" alt="OpenAI Responses & Chat API" />
    <img src="https://img.shields.io/badge/ComfyUI-v0.46.0-7c3aed?style=flat-square" alt="ComfyUI v0.46.0" />
  </p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README.zh-CN.md">简体中文</a> ·
    <a href="README.zh-TW.md">繁體中文（台灣）</a> ·
    <a href="README.zh-HK.md">繁體中文（香港）</a> ·
    <strong>日本語</strong> ·
    <a href="README.ko-KR.md">한국어</a>
  </p>
</div>

---

ColabOne は、**macOS (Apple Silicon)** および **Windows (x64)** 向けの高速なネイティブデスクトップアプリから、リモートの Google Colab GPU を手軽に扱えるワークスペースです。ノートブック操作なしで、複数 GPU の起動・管理、ハードウェア負荷と課金残高のリアルタイム監視、プライベートな OpenAI 互換 LLM エンドポイントの公開、ComfyUI ワークフローの実行をまとめて行えます。

お使いの PC 上で動作するのは、インプロセスコントローラを内蔵した単一のピュア Go ネイティブデスクトップバイナリ（`ColabOne.app` / `ColabOne.exe`）のみです — ローカルへの Python ランタイム導入は不要です。モデルのダウンロード、GGUF 重みファイルの保持、`llama.cpp` による推論、ComfyUI の実行はすべてリモートの Colab GPU 上で完結します（起動時に自動アップロードされる埋め込み Python 制御サービスによって駆動されます）。

## ✨ 主な機能

- **マルチ GPU 管理とリアルタイム監視（`リモートデバイス`）** — 複数の Colab GPU（`T4`、`L4`、`A100`、`H100`、`G4` RTX PRO 6000）を起動・切り替え・選択解放でき、同じ Google アカウント上で外部起動されたインスタンスの状態も確認できます。課金ステータス、`CU/時` の消費レート、残り CU 残高、連続稼働目安、リージョン国旗アイコンに加え、GPU VRAM／負荷・システムメモリ (RAM)・ディスク I/O・CPU の 60 秒リアルタイムグラフを監視できます。
- **スマートアイドル自動解放** — 設定可能なアイドルカウントダウン（デフォルト **30 分**、`15 分`〜`2 時間` から選択可能）で不要なコンピュート残高の消費を防ぎます。保護機能はリモート VM 側で実行されるため ColabOne を終了しても継続して動作し、ダウンロード・VRAM への読み込み・API 推論・ComfyUI の実行中は自動で一時停止します。
- **OpenAI 互換 LLM ゲートウェイ（`外部 API`）** — 内蔵の **モデル選択** パネルから Hugging Face の GGUF モデルを直接検索・読み込みできます。リアルタイムの VRAM・ディスク適合判定、VRAM + RAM 自動ハイブリッド読み込み（`--fit on`）、16／32 スレッド並列の SHA-256 検証付きダウンロード、`8K`〜`128K` のコンテキストプリセットに対応。ご自身の Cloudflare Named Tunnel 経由で `/v1/models`、`/v1/chat/completions`、`/v1/responses`、`/v1/responses/compact` を公開できます。
- **ComfyUI ワークフロー工房とネイティブキャンバス（`ComfyUI`）** — リモートの **ComfyUI `v0.46.0`** を 2 つのモードで利用できます。**ワークフロー工房** では任意の ComfyUI API JSON から操作しやすいパラメータフォームを自動生成し、SHA-256 検証済みのプレビューやローカル保存が可能。**ネイティブキャンバス** ではトークン認証付きトンネル経由で、専用ウィンドウまたはブラウザ上にフル機能のノードエディタを開けます。
- **Google ドライブへの自動永続化** — アプリ専用スコープの `drive.file` 権限を使い、モデル一覧・ComfyUI ワークフロー・生成物を `My Drive / ColabOne_Drive` へ自動保存します。標準では Colab ネイティブ DriveFS によりすぐ利用でき（新規 VM ごとに 1 クリックで許可）、無料の Google Cloud 利用規約に一度同意すれば、確認不要の永久ヘッドレス `rclone` 自動マウントへアップグレードできます。
- **日常利用に配慮した設計** — ピュア Go 単一バイナリ構成（MyGo `v0.10.0` + インプロセス Go コントローラ。ローカル Python ランタイムや同階層の `runtime/`・`site-packages/` フォルダは一切不要で、ローカルへの Python・Go・Node 導入も不要）、6 言語 UI、ライト／ダーク／システムに従うのテーマ切替、OS キーチェーンによる認証情報保護、任意設定の GPU 割り当て専用プロキシ、ワンクリックのリモートサービスホットアップデート、機密情報をマスク済みのリアルタイム実行ログパネル（`Ctrl+J`）を備えています。

## 🚀 クイックスタート

ColabOne は 4 つのサイドバー画面（**リモートデバイス → 外部 API → ComfyUI → 設定**）と、右側から開く **実行ログ** パネルで構成されています。

1. **ダウンロードとインストール**
   [Releases](https://github.com/luolongfei/ColabOne/releases) からお使いの環境向けインストーラーをダウンロード（または[ソースからビルド](#-ソースからビルド)）します。Windows では `.exe` インストーラーを実行（ユーザー単位でインストール、管理者権限不要）、macOS では `.dmg` ディスクイメージを開いて `ColabOne.app` を **アプリケーション** フォルダへドラッグしてください。ColabOne は自己完結型のピュア Go 単一バイナリアプリのため、ローカルの Python ランタイムや同階層の `runtime/`・`site-packages/` フォルダは不要です。Windows では **設定** からも管理者権限なしでスタートメニューおよびデスクトップのショートカットを管理できます。

2. **Google アカウントでログイン**
   **設定** → **Google でログイン** を開き、ブラウザでアカウントを認可すると自動でログインが完了します。リダイレクトされずブラウザに認可ページが表示された場合は、手動貼り付け欄を開いて **貼り付けてログイン完了** をクリックしてください。認証情報は macOS キーチェーンまたは Windows 資格情報マネージャーに暗号化保存され、再起動後も自動で復元・更新されます。

3. **GPU を起動**
   **リモートデバイス** を開き、GPU モデル（デフォルトは `G4`）とアイドル自動解放時間（デフォルト **30 分**）を選択して **GPU を起動** をクリックします。準備が整うと、上部バーと概要カードに連続稼働目安、インスタンス所在地、ハードウェア監視グラフ、Google ドライブのマウント状態がリアルタイム表示されます。

4. **LLM の公開または ComfyUI の実行**
   - **テキスト API**: **外部 API** → **モデル選択** を開き、**マイモデル**（編集可能な初期 Hugging Face リポジトリを事前登録済み）から GGUF モデルを選ぶか、**コミュニティ** で検索します。**取得して読込** をクリックすると整合性検証済みの分割ファイルをリモートへダウンロードして `llama-server` へ読み込みます。続いて Cloudflare Named Tunnel を設定し、API サービスを開始します。
   - **画像・動画・マルチモーダルワークフロー**: **ComfyUI** を開き、**ワークフロー工房** にワークフローを取り込むか、**ネイティブキャンバス** でフル機能のノードエディタを起動します。

5. **作業終了後の解放**
   各インスタンスの **個別に停止** をクリックするか、**すべて停止** から停止対象をチェックボックスで選択して解放します（チェックを外したインスタンスは稼働を継続します）。本アプリ管理のインスタンスは解放前に公開トンネルを閉じ、`ColabOne_Drive` への同期を自動で完了します。

## 🌐 OpenAI 互換の外部 API

**外部 API** は、内蔵の単一モデル GGUF ピッカーと、リモートの `llama-server`（`127.0.0.1:8081`）をバックエンドとする Bearer 認証付き専用 OpenAI ゲートウェイ（`127.0.0.1:7861`）を組み合わせた機能です。管理ルート（`/api/v1/*`）は非公開の制御ポートに分離されており、外部へ公開されることはありません。

### 📡 対応エンドポイント

| メソッド | エンドポイント | 説明 |
|---|---|---|
| `GET` | `/v1/models` `/v1/models/{id}` | リモート VRAM に現在読み込まれている GGUF モデル情報（有効なコンテキスト長を含む）を返します。 |
| `POST` | `/v1/chat/completions` | `stream=true/false`、ツール呼び出し、`developer` ロールに対応した OpenAI Chat Completions API。 |
| `POST` | `/v1/responses` | SSE ストリーミング、function／custom（`apply_patch`）ツール、推論サマリー、3 段階の自動コンテキスト圧縮に対応した OpenAI Responses API（`wire_api = "responses"`）。 |
| `POST` | `/v1/responses/compact` | 長文のコーディングスレッド向け Codex／OpenAI 互換コンテキスト圧縮エンドポイント（`response.compaction`）。 |

### 🔌 外部クライアントからの接続

1. **固定ドメイントンネルを設定**: Cloudflare Zero Trust で Cloudflared Named Tunnel を作成し、公開 HTTPS ホスト名の転送先を `http://127.0.0.1:7861` に設定します（API ホスト名に対して Cloudflare Access の対話型ログインは有効にしないでください）。発行されたインストールコマンドまたは Tunnel Token を **外部 API** に貼り付けて保存し、**API サービスを開始** をクリックします（*一時ドメイン `trycloudflare.com` は意図的に無効化されています*）。
2. **アプリや SDK から ColabOne へ接続**: 画面に表示される **Base URL**、**API Key**、**現在のモデル ID**（VRAM を再読み込みせずに即時変更可能。現在読み込み中のモデルへ自動ルーティングする固定エイリアス `colabone/auto` も利用できます）をコピーしてクライアントへ設定します。アプリ内には **cURL**・**Python**・**Codex** のコード例と、リクエストごとのトークン生成速度（`tok/s`、TTFT、プリフィル速度）がリアルタイム表示されます。

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="https://api.example.com/v1",
    api_key=os.environ["COLABONE_API_KEY"],
)

stream = client.responses.create(
    model="colabone/auto",
    input="Hello from ColabOne!",
    stream=True,
)
for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
```

**外部 API** を停止すると公開アクセスのみが遮断され、GPU インスタンスと読み込み済みモデルはそのまま維持されます。GPU を停止すると、ゲートウェイとトンネルも自動で終了します。エンドポイント仕様とエラーコードの詳細は [docs/api.md](docs/api.md#public-inference-api-llm-openai-gateway) を参照してください。

## 🎨 ComfyUI ワークスペース

ColabOne は **ComfyUI `v0.46.0`** をリモートの Colab GPU 上（`127.0.0.1:8188`）のみにデプロイし、2 層構成のワークスペースを提供します。

- **ワークフロー工房**
  - ComfyUI の **Save (API Format)** JSON ワークフローを取り込み（GPU 起動前のオフライン状態でも可能）、型付きのパラメータ入力フォームを自動生成します。
  - 参照用の画像・音声をアップロードし、途中でキャンセル可能なリモートキュー実行を送信して、ノードごとの進捗をリアルタイムで確認できます。
  - SHA-256 検証済みの画像・動画・音声出力をプレビューし、OS ネイティブの保存ダイアログからローカルへ保存可能。ワークフロー（`/workflows/**`）と出力作品（`/comfyui-outputs/**`）は `ColabOne_Drive` へ自動同期されます。
  - `catalog/comfyui.json`（リビジョン固定および SHA-256 検証付きのデータ専用マニフェスト）により、リモートモデルやワークフローパックを宣言的に管理できます。
- **ネイティブキャンバス**
  - 固定ドメインの Cloudflare Named Tunnel の背後に、トークン認証付きの HTTP + WebSocket リバースプロキシ（`127.0.0.1:8189` → `8188`）を構築します。
  - セッションごとに生成されるランダムトークンをスコープ限定の `HttpOnly` Cookie へ交換し、専用の **ComfyUI ネイティブキャンバス** ウィンドウまたはシステムブラウザで直接開きます。
- **厳格な VRAM 排他制御**
  - `llama-server`（`8081`）と `ComfyUI`（`8188`）が GPU VRAM を同時に占有することはありません。切り替え時には稼働中エンジンを自動アンロードしつつ、リモートディスク上のキャッシュ済み重みは保持します。
  - ComfyUI の実行キューやネイティブキャンバスへのリクエストがアクティブな間は、アイドル自動解放のカウントダウンが自動で一時停止します。

## 🔒 ストレージ・課金・セキュリティ

- **Google ドライブ同期**: モデル一覧・ComfyUI ワークフロー・出力作品は制限付きスコープ `drive.file` の下で `My Drive / ColabOne_Drive` に永続保存されます（ColabOne が無関係な個人ドライブファイルへアクセスすることはありません）。モデルの重みファイルは Colab の高速な一時ディスクに配置され、新しい VM を起動した際に再ダウンロードできます。モデルのアンロードや切り替えで稼働中インスタンス上のキャッシュファイルが削除されることはありません。
- **システムトレイと自動解放**: ウィンドウを閉じると ColabOne はシステムトレイへ最小化されます。アプリを終了するとローカルのハートビートは止まりますが、リモート VM 上のアイドル自動解放保護は継続して動作します。通信断だけでは Colab の課金は停止しないため、必ず **リモートデバイス** 画面で停止済みを確認してください。
- **課金の見積もり**: 表示される残り CU 残高・`CU/時` の消費レート・連続稼働目安は直近の Colab 照会結果に基づいており、公式の請求明細とは時間差が生じる場合があります。
- **認証情報とプライバシー**: Google OAuth トークン、API キー、Cloudflare Tunnel Token、任意の Hugging Face Token（アクセス制限リポジトリ用）、任意設定の GPU 割り当て専用プロキシは OS キーチェーン／資格情報ストアに保存され、ログからは自動的にマスクされます（メモリ上に最新 300 件を保持し、ローカルには権限 `0600` のアクティビティログを記録）。既存の Colab CLI プロファイルとは完全に独立して動作します。
- **対象範囲と整合性検証**: GGUF インポート時は 40 文字のコミット SHA を厳密に固定し、リポジトリ内の Python コードを一切実行せずに全分割ファイルを SHA-256 で検証します。従来のデスクトップ内蔵チャット／画像／動画ページは廃止され、**外部 API** と **ComfyUI** に統合されました。エージェント機能およびローカル MCP 実行は v0.1 の対象外です。開発ビルドには商用コード署名および公証が付与されていないため、必要に応じてソースコードを確認のうえローカルでビルドしてください。

## 🔧 ソースからビルド

### 📋 前提条件

- **Go** `1.27.3`
- **Node.js** `24 LTS`（または `26`）
- **Python** `3.12`・**`uv`**（または標準の `venv` + `pip`）— ビルド／テスト時のみ使用（埋め込みリモートサービスバンドルの同期、リモートサービス用 Python テスト、パッケージ化スクリプトの実行）

### 🔨 セットアップとビルド

```sh
uv venv --python 3.12
uv pip install --python .venv/bin/python -r requirements-dev.txt
.venv/bin/python tools/sync_service_bundle.py
npm --prefix frontend ci
cd console
go tool mygo generate
cd ..
npm --prefix frontend run build
```

### 📦 検証とパッケージ化

> Windows では `.venv/bin/python` を `.venv\Scripts\python.exe` に読み替えてください。

```sh
# すべてのオフライン検証を実行（埋め込みサービス同期・Go vet/test・リモート Python pytest・Vitest・フロントエンドビルド）
.venv/bin/python tools/install.py --root work/check --check

# ネイティブのピュア Go インストーラー（Windows は .exe、macOS は .dmg）を dist/ にビルド
.venv/bin/python tools/package.py --out dist
```

ネイティブインストーラー（`ColabOne-<version>-windows-x64-setup.exe` / `ColabOne-<version>-macos-arm64.dmg`）は `dist/` に出力され（展開済みステージングディレクトリは `work/package-stage/Programs/ColabOne`、ポータブル `.zip` も必要な場合は `--portable-zip` を指定）、ビルド済み Vue フロントエンドとリモート Python 制御サービス（`server/`、`common/`、`catalog/`）を `go:embed` で内蔵した単独のピュア Go バイナリ（`ColabOne.app` / `ColabOne.exe`）を収録します。エンドユーザー側でローカル Python ランタイムや同階層の依存フォルダを用意する必要はありません。

すべての単体テストおよびブラウザテストはモックを使って 100% オフラインで動作し、Google や Hugging Face へ接続したり GPU を割り当てたりすることはありません。実 GPU による受け入れ検証は厳格な計算予算の下で明示的に実施しています。詳細は [docs/validation.md](docs/validation.md) を参照してください。

## 📚 ドキュメント

- [アーキテクチャ概要](docs/architecture.md) — ピュア Go インプロセスコントローラ、埋め込みリモートサービス、ループバックポート、Google ドライブ同期、テレメトリ、セキュリティ境界
- [リモートおよび外部 API リファレンス](docs/api.md) — 制御ルート、OpenAI ゲートウェイ仕様、ComfyUI エンドポイント
- [検証記録](docs/validation.md) — オフライン CI カバレッジと実 GPU 受け入れテストログ
- [アップストリーム出典情報](third_party/SOURCES.md) — バージョン固定済みサードパーティ依存関係と帰属表示

## 🤝 コントリビューション

コードの貢献、バグ報告、機能提案はいつでも歓迎しています。ローカル環境のセットアップ、PR ガイドライン、オフラインテストの要件については [コントリビューションガイド](CONTRIBUTING.md) をご覧ください。

### 💖 コントリビューター

ColabOne の開発と改善に協力してくださった皆様に感謝いたします。

<a href="https://github.com/luolongfei/ColabOne/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=luolongfei/ColabOne" alt="ColabOne コントリビューター" />
</a>

## 🎉 謝辞

**[Alma](https://alma.now/)** および **[Antigravity-Manager](https://github.com/lbjlaq/Antigravity-Manager)** に特別な感謝を捧げます — ColabOne の UI デザイン、ビジュアルの洗練、さらにはアプリロゴに至るまで、両プロジェクトから直接的な着想と参考を得ています。

あわせて以下のオープンソースプロジェクトにも心より感謝いたします。これら素晴らしいオープンソースの知見と基盤があってこそ、ColabOne は誕生しました（詳細は [NOTICE](NOTICE) および [third_party/SOURCES.md](third_party/SOURCES.md) を参照）：

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)** *(Apache-2.0)* — Google Colab の GPU 割り当て、ランタイム起動確認、ハートビートによるライフサイクル管理の設計で参考にさせていただきました。
- **[EasyColab](https://github.com/wy51ai/easycolab)** *(Apache-2.0)* — デスクトップアプリと Colab 間の制御構成およびヘッドレス `rclone` による Google ドライブマウント手法の設計で参考にさせていただきました。
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)** *(MIT)* — モデルマニフェスト、バージョン固定済み `llama.cpp` CUDA デプロイ、並列レンジダウンロード戦略の設計で参考にさせていただきました。
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)** *(MIT)*・**[ComfyUI](https://github.com/Comfy-Org/ComfyUI)** (`v0.46.0`, GPL-3.0) — リモートの GGUF LLM 推論エンジンおよびノードベース画像・動画・マルチモーダルワークフローエンジンとして統合しています。
- **デスクトップ・UI スタック** — **[MyGo](https://github.com/egoist/mygo)** (`v0.10.0`, MIT)、**[coder/websocket](https://github.com/coder/websocket)** *(ISC)*、**[Vue 3](https://github.com/vuejs/core)**、**[Vite](https://github.com/vitejs/vite)**、**[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**、**[Phosphor Icons](https://github.com/phosphor-icons/vue)**、**[Shiki](https://github.com/shikijs/shiki)**、**[flag-icons](https://github.com/lipis/flag-icons)** *(MIT)*、および **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)** *(SIL OFL 1.1)*。
- **リモートサービス・トンネル・ドライブ基盤** — **[FastAPI](https://github.com/fastapi/fastapi)** *(MIT)*、**[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)** *(Apache-2.0)*、**[cloudflared](https://github.com/cloudflare/cloudflared)** *(Apache-2.0)*、および **[rclone](https://github.com/rclone/rclone)** *(MIT)*。

## 📜 ライセンス

ColabOne は、リモート統合している [ComfyUI](https://github.com/Comfy-Org/ComfyUI)（`v0.46.0`、GPL-3.0）に合わせ、**GNU General Public License v3.0**（[LICENSE](LICENSE)）の下でライセンスされています。すべてのアップストリームライセンスおよび著作権表記は [NOTICE](NOTICE) と [third_party/SOURCES.md](third_party/SOURCES.md) に保持されています。
