<div align="center">
  <img src="console/resources/icon.png" width="96" height="96" alt="ColabOne" />
  <h1>ColabOne</h1>
  <p><strong>Google Colab GPU를 위한 경량 데스크톱 워크스페이스 — 원격에서 OpenAI 호환 GGUF LLM을 서빙하고 ComfyUI 워크플로를 실행하세요.</strong></p>

  <p>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-2563eb?style=flat-square" alt="License: GPL-3.0" /></a>
    <img src="https://img.shields.io/badge/Platform-macOS%20Apple%20Silicon%20%7C%20Windows%20x64-475569?style=flat-square" alt="Platform: macOS Apple Silicon | Windows x64" />
    <img src="https://img.shields.io/badge/OpenAI%20API-Responses%20%26%20Chat-0f766e?style=flat-square" alt="OpenAI Responses & Chat API" />
    <img src="https://img.shields.io/badge/ComfyUI-v0.46.0-7c3aed?style=flat-square" alt="ComfyUI v0.46.0" />
  </p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README.zh-CN.md">简体中文</a> ·
    <a href="README.zh-TW.md">繁體中文（台灣）</a> ·
    <a href="README.zh-HK.md">繁體中文（香港）</a> ·
    <a href="README.ja-JP.md">日本語</a> ·
    <strong>한국어</strong>
  </p>
</div>

---

ColabOne은 **macOS (Apple Silicon)** 및 **Windows (x64)** 환경에서 빠른 네이티브 데스크톱 앱으로 원격 Google Colab GPU를 바로 다룰 수 있게 해줍니다. 노트북을 직접 조작할 필요 없이 여러 대의 GPU를 시작·관리하고, 실시간 하드웨어 부하와 잔액 기준 예상 가동 시간을 모니터링하며, 개인용 OpenAI 호환 LLM 엔드포인트와 ComfyUI 워크플로를 손쉽게 실행할 수 있습니다.

내 컴퓨터에서는 인프로세스 컨트롤러가 내장된 단일 순수 Go 네이티브 데스크톱 바이너리(`ColabOne.app` / `ColabOne.exe`)만 실행되며, 로컬 Python 런타임이 전혀 필요하지 않습니다. 모든 모델 다운로드, GGUF 가중치, `llama.cpp` 추론 및 ComfyUI 실행은 오직 원격 Colab GPU에서만 처리됩니다(시작 시 자동 업로드되는 내장 Python 제어 서비스 기반).

## ✨ 주요 기능

- **멀티 GPU 관리 및 실시간 텔레메트리 (`원격 기기`)** — 여러 대의 Colab GPU(`T4`, `L4`, `A100`, `H100`, `G4` RTX PRO 6000)를 시작·전환하고 원하는 인스턴스만 선택적으로 해제할 수 있으며, Google 계정에서 실행 중인 외부 인스턴스도 함께 조회할 수 있습니다. 실시간 과금 상태, `CU/시간` 소모 속도, 남은 CU 잔액, 예상 사용 가능 시간, 인스턴스 리전 국기와 함께 GPU VRAM·코어 부하, 시스템 RAM, 디스크 I/O, CPU의 60초 실시간 차트를 한눈에 확인합니다.
- **스마트 유휴 자동 해제** — 설정 가능한 유휴 카운트다운(기본값 **30분**, `15분`~`2시간` 선택 가능)으로 컴퓨팅 잔액을 보호합니다. 원격 VM에서 직접 동작하므로 ColabOne을 종료해도 보호가 유지되며, 모델 다운로드, VRAM 로드, API 추론, ComfyUI 작업 중에는 카운트다운이 자동으로 일시 정지됩니다.
- **OpenAI 호환 LLM 게이트웨이 (`외부 API`)** — 내장 **모델 선택** 패널에서 Hugging Face GGUF 모델을 직접 탐색하고 로드할 수 있습니다. 실시간 VRAM 및 디스크 적합성 검사, 자동 VRAM + RAM 하이브리드 로드(`--fit on`), 16/32스레드 병렬 다운로드 및 SHA-256 무결성 검증, `8K`~`128K` 컨텍스트 프리셋을 지원합니다. 개인 Cloudflare Named Tunnel을 통해 `/v1/models`, `/v1/chat/completions`, `/v1/responses`, `/v1/responses/compact` 엔드포인트를 안전하게 공개할 수 있습니다.
- **ComfyUI 워크플로 스튜디오 및 네이티브 캔버스 (`ComfyUI`)** — 원격 **ComfyUI `v0.46.0`** 버전을 두 가지 모드로 실행합니다. **워크플로 스튜디오**는 ComfyUI API JSON을 깔끔한 파라미터 입력 폼으로 자동 변환하고 SHA-256으로 검증된 미리보기와 로컬 내보내기를 제공하며, **네이티브 캔버스**는 토큰 인증 터널을 통해 전용 앱 창이나 브라우저에서 전체 노드 편집기를 바로 열어줍니다.
- **Google 드라이브 자동 영구 보관** — 앱 전용 `drive.file` 권한으로 모델 목록, ComfyUI 워크플로, 생성 결과물을 `My Drive / ColabOne_Drive`에 자동 저장합니다. 별도 설정 없이 Colab 네이티브 DriveFS(새 VM당 1회 클릭)로 바로 동작하며, 무료 Google Cloud 서비스 약관에 한 번 동의하면 팝업 없는 영구 헤드리스 `rclone` 자동 마운트로 업그레이드됩니다.
- **일상 사용에 맞춘 설계** — 순수 Go 단일 바이너리 데스크톱 앱(MyGo `v0.10.0` + 인프로세스 Go 컨트롤러; 로컬 Python 런타임이나 동일 경로의 `runtime/`·`site-packages/` 폴더 불필요, 로컬에 Python·Go·Node.js 설치 불필요)으로 제공되며, 6개 UI 언어, 라이트/다크/시스템 설정 테마, OS 키체인 자격 증명 보관, 선택형 GPU 할당 전용 프록시, 원클릭 원격 서비스 핫 업데이트, 민감 정보가 자동 마스킹되는 실시간 **실행 로그** 패널(`Ctrl+J`)을 지원합니다.

## 🚀 빠른 시작

ColabOne은 **원격 기기 → 외부 API → ComfyUI → 설정**의 4개 사이드바 화면과 우측 슬라이드 오버 **실행 로그** 패널로 구성되어 있습니다.

1. **다운로드 및 실행**
   [Releases](https://github.com/luolongfei/ColabOne/releases)에서 플랫폼에 맞는 패키지를 다운로드하고(또는 [소스에서 직접 빌드](#-소스에서-빌드하기)), 압축을 푼 뒤 `ColabOne.app`(macOS) 또는 `ColabOne.exe`(Windows)를 실행합니다. ColabOne은 완전히 독립 실행되는 순수 Go 단일 바이너리 앱이므로 로컬 Python 런타임이나 동일 경로의 `runtime/`·`site-packages/` 폴더가 필요하지 않습니다. Windows에서는 관리자 권한 없이 **설정**에서 시작 메뉴 및 바탕 화면 바로 가기를 만들 수 있습니다.

2. **Google 계정 로그인**
   **설정** → **Google 로그인**으로 이동해 브라우저에서 계정을 승인하면 ColabOne이 자동으로 로그인을 완료합니다. 브라우저가 자동으로 리디렉션되지 않고 인증 페이지를 표시하는 경우, 직접 붙여넣기 섹션을 펼치고 **붙여넣고 로그인 완료**를 클릭하세요. 자격 증명은 macOS 키체인 또는 Windows 자격 증명 관리자에 암호화되어 저장되며 재시작 시에도 자동으로 갱신됩니다.

3. **GPU 시작**
   **원격 기기**에서 GPU 모델(기본값 `G4`)과 유휴 자동 해제 시간(기본값 **30분**)을 선택한 뒤 **GPU 시작**을 클릭합니다. 준비가 완료되면 상단 바와 요약 카드에 실시간 과금 기준 예상 가동 시간, 인스턴스 위치, 하드웨어 텔레메트리 차트 및 Google 드라이브 마운트 상태가 표시됩니다.

4. **LLM 서빙 또는 ComfyUI 실행**
   - **텍스트 API**: **외부 API** → **모델 선택**을 열어 **내 모델** (편집 가능한 기본 Hugging Face 저장소 사전 등록)에서 GGUF 모델을 선택하거나 **커뮤니티**에서 검색합니다. **다운로드 및 로드**를 클릭하면 원격에서 검증된 분할 파일을 받아 `llama-server`에 로드하며, 이후 Cloudflare Named Tunnel을 설정해 API 서비스를 시작할 수 있습니다.
   - **이미지·비디오·멀티모달 워크플로**: **ComfyUI**에서 **워크플로 스튜디오**로 워크플로를 가져오거나 **네이티브 캔버스**에서 전체 노드 편집기를 실행합니다.

5. **사용 종료 후 리소스 해제**
   특정 인스턴스의 **개별 중지**를 클릭하거나, **모두 중지**를 클릭해 종료할 인스턴스만 선택적으로 체크하면 다른 세션은 유지한 채 원하는 인스턴스만 해제할 수 있습니다. 이 앱에서 관리하는 인스턴스는 해제 전에 공개 터널을 자동으로 닫고 `ColabOne_Drive` 동기화를 마무리합니다.

## 🌐 OpenAI 호환 외부 API

**외부 API**는 내장 단일 모델 GGUF 선택기와 원격 `llama-server`(`127.0.0.1:8081`) 기반의 전용 Bearer 인증 OpenAI 게이트웨이(`127.0.0.1:7861`)를 결합합니다. 관리용 라우트(`/api/v1/*`)는 내부 전용 제어 포트에 격리되어 외부에 절대 노출되지 않습니다.

### 📡 지원 엔드포인트

| 메서드 | 엔드포인트 | 설명 |
|---|---|---|
| `GET` | `/v1/models` `/v1/models/{id}` | 현재 원격 VRAM에 로드된 GGUF 모델 정보(활성 컨텍스트 길이 포함)를 조회합니다. |
| `POST` | `/v1/chat/completions` | `stream=true/false`, 도구 호출, `developer` 역할을 지원하는 OpenAI Chat Completions API입니다. |
| `POST` | `/v1/responses` | SSE 스트리밍, function 및 custom/`apply_patch` 도구, 추론 요약, 3단계 자동 컨텍스트 압축을 지원하는 OpenAI Responses API(`wire_api = "responses"`)입니다. |
| `POST` | `/v1/responses/compact` | 긴 코딩 대화를 위한 Codex / OpenAI 컨텍스트 압축(`response.compaction`) 엔드포인트입니다. |

### 🔌 외부 클라이언트 연결

1. **고정 도메인 터널 설정**: Cloudflare Zero Trust에서 Cloudflared Named Tunnel을 생성하고 공개 HTTPS 호스트 이름을 `http://127.0.0.1:7861`로 매핑합니다(API 호스트 이름에는 Cloudflare Access 대화형 로그인을 활성화하지 마세요). 설치 명령어 또는 Tunnel Token을 **외부 API**에 붙여넣고 **설정 저장**을 누른 뒤 **API 서비스 시작**을 클릭합니다. *(임시 `trycloudflare.com` 도메인은 의도적으로 비활성화되어 있습니다.)*
2. **앱 또는 SDK에서 ColabOne 연결**: **Base URL**, **API Key**, **현재 모델 ID** (VRAM 재로드 없이 실시간 변경 가능 — 또는 현재 로드된 모델로 자동 라우팅되는 고정 별칭 `colabone/auto` 사용)를 복사해 연결합니다. 앱 내에서 **cURL**, **Python**, **Codex** 호출 예제와 요청별 실시간 토큰 처리량(`tok/s`, TTFT, 프리필 속도)을 바로 확인할 수 있습니다.

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

**외부 API**를 중지하면 GPU와 로드된 모델은 그대로 유지한 채 외부 접근만 차단되며, GPU 인스턴스를 중지하면 게이트웨이와 터널도 함께 자동 종료됩니다. 전체 라우트 명세와 오류 코드는 [docs/api.md](docs/api.md#public-inference-api-llm-openai-gateway)를 참고하세요.

## 🎨 ComfyUI 워크스페이스

ColabOne은 원격 Colab GPU(`127.0.0.1:8188`)에만 **ComfyUI `v0.46.0`** 버전을 배포하며 이중 레이어 워크스페이스를 제공합니다.

- **워크플로 스튜디오**
  - GPU를 시작하기 전 오프라인 상태에서도 ComfyUI의 **Save (API Format)** JSON 워크플로를 가져와 타입별 파라미터 입력 폼을 자동 생성할 수 있습니다.
  - 참조 이미지나 오디오를 업로드하고 취소 가능한 원격 대기열 작업을 제출하며, 노드별 진행 상황을 실시간으로 추적합니다.
  - SHA-256으로 검증된 이미지·비디오·오디오 결과물을 미리 보고 네이티브 저장 대화상자로 로컬에 내보낼 수 있으며, 워크플로(`/workflows/**`)와 결과물(`/comfyui-outputs/**`)은 `ColabOne_Drive`에 자동 동기화됩니다.
  - `catalog/comfyui.json`(고정 리비전과 SHA-256 검증을 포함한 순수 데이터 매니페스트)을 통해 원격 모델 및 워크플로 팩을 선언적으로 관리할 수 있습니다.
- **네이티브 캔버스**
  - 고정 도메인 Cloudflare Named Tunnel 뒤에서 토큰 인증 기반 HTTP + WebSocket 리버스 프록시(`127.0.0.1:8189` → `8188`)를 실행합니다.
  - 세션별 무작위 토큰을 범위가 제한된 `HttpOnly` 쿠키로 교환해 전용 **ComfyUI 네이티브 캔버스** 앱 창이나 시스템 브라우저에서 바로 열 수 있습니다.
- **엄격한 VRAM 상호 배제**
  - `llama-server`(`8081`)와 `ComfyUI`(`8188`)는 GPU VRAM을 동시에 점유하지 않습니다. 엔진을 전환하면 원격 디스크의 캐시된 가중치는 유지한 채 현재 활성 엔진을 자동으로 언로드합니다.
  - 진행 중인 ComfyUI 대기열 작업과 네이티브 캔버스 요청은 유휴 자동 해제 카운트다운을 자동으로 일시 정지합니다.

## 🔒 저장소, 과금 및 보안

- **Google 드라이브 동기화**: 모델 목록, ComfyUI 워크플로, 생성 결과물은 권한이 제한된 `drive.file` 범위로 `My Drive / ColabOne_Drive`에 영구 보관됩니다(ColabOne은 무관한 개인 드라이브 파일에 접근할 수 없습니다). 모델 가중치는 Colab의 고속 임시 디스크에 저장되며 새 VM을 시작할 때마다 다시 다운로드할 수 있습니다. 모델을 언로드하거나 전환해도 활성 인스턴스의 캐시 파일은 삭제되지 않습니다.
- **시스템 트레이 및 자동 해제**: 창을 닫으면 ColabOne은 시스템 트레이로 최소화됩니다. 앱을 종료하면 로컬 하트비트는 중지되지만 원격 VM의 유휴 자동 해제 보호는 계속 작동합니다. 네트워크 연결이 끊어져도 Colab 과금은 중단되지 않으므로 항상 **원격 기기**에서 중지 상태를 확인하세요.
- **과금 추정치 안내**: 표시되는 CU 잔액, `CU/시간` 소모 속도 및 예상 사용 가능 시간은 최근 Colab 조회 결과를 기반으로 하며, 공식 과금 내역과 시차가 발생할 수 있습니다.
- **자격 증명 및 개인정보 보호**: Google OAuth 토큰, API 키, Cloudflare Tunnel 토큰, 선택형 Hugging Face 토큰(승인 필요 저장소용), 선택형 GPU 할당 전용 프록시 설정은 모두 OS 키체인에 보관되며 로그(메모리 최근 300건 및 로컬 `0600` 권한 활동 로그)에서 민감 정보가 자동 마스킹됩니다. ColabOne은 기존 Colab CLI 프로필과 완전히 독립적으로 동작합니다.
- **지원 범위 및 검증**: GGUF 가져오기는 40자리 커밋 SHA를 정확히 고정하고 저장소의 Python 코드를 실행하지 않은 채 모든 분할 파일을 SHA-256으로 검증합니다. 기존의 내장 데스크톱 채팅·이미지·비디오 페이지는 **외부 API**와 **ComfyUI**로 대체되었습니다. 에이전트 및 로컬 MCP 실행은 v0.1 범위에서 제외됩니다. 개발 빌드는 상용 코드 서명 또는 공증이 적용되어 있지 않으므로 언제든 소스를 직접 검토하거나 로컬에서 빌드해 사용할 수 있습니다.

## 🔧 소스에서 빌드하기

### 📋 사전 요구 사항

- **Go** `1.27.3`
- **Node.js** `24 LTS` (또는 `26`)
- **Python** `3.12` + **`uv`** (또는 표준 `venv` + `pip`) — 빌드 및 테스트 단계 전용(내장 원격 서비스 번들 동기화, 원격 서비스 Python 테스트, 패키징 스크립트 실행)

### 🔨 환경 구성 및 빌드

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

### 📦 검증 및 패키징

> Windows에서는 `.venv/bin/python` 대신 `.venv\Scripts\python.exe`를 사용하세요.

```sh
# 모든 오프라인 검사 실행 (내장 서비스 번들 동기화, Go vet/test, 원격 Python pytest, Vitest, 프론트엔드 빌드)
.venv/bin/python tools/install.py --root work/check --check

# dist/ 디렉터리에 순수 Go 단일 바이너리 포터블 패키지 빌드
.venv/bin/python tools/package.py --out dist
```

포터블 패키지는 `dist/`에 생성되며(압축 해제된 스테이징 디렉터리는 `work/package-stage/Programs/ColabOne`), 컴파일된 Vue 프론트엔드와 원격 Python 제어 서비스(`server/`, `common/`, `catalog/`)가 `go:embed`로 내장된 독립 실행형 순수 Go 바이너리(`ColabOne.app` / `ColabOne.exe`)를 제공하므로 최종 사용자가 로컬 Python 런타임이나 동일 경로의 의존성 폴더를 따로 둘 필요가 없습니다.

모든 단위 테스트와 브라우저 테스트는 목(mock)을 사용해 100% 오프라인으로 실행되며, Google이나 Hugging Face에 접속하거나 GPU를 할당하지 않습니다. 실제 GPU 환경의 인수 검증은 엄격한 컴퓨팅 예산 하에 명시적으로만 수행합니다. 자세한 내용은 [docs/validation.md](docs/validation.md)를 참고하세요.

## 📚 문서

- [아키텍처 개요](docs/architecture.md) — 순수 Go 인프로세스 컨트롤러, 내장 원격 서비스, 루프백 포트, 드라이브 동기화, 텔레메트리 및 보안 경계
- [원격 및 외부 API 레퍼런스](docs/api.md) — 제어 라우트, OpenAI 게이트웨이 명세 및 ComfyUI 엔드포인트
- [검증 기록](docs/validation.md) — 오프라인 CI 커버리지 및 실제 GPU 인수 검증 로그
- [업스트림 출처 및 라이선스](third_party/SOURCES.md) — 버전이 고정된 서드파티 의존성 및 저작권 고지

## 🤝 기여하기

기여, 버그 제보, 기능 제안은 언제나 환영합니다! 로컬 개발 환경 구성, PR 가이드라인 및 오프라인 테스트 요구 사항은 [기여 가이드](CONTRIBUTING.md)를 참고하세요.

### 💖 기여자

ColabOne을 함께 만들고 개선해 주신 모든 분들께 감사드립니다:

<a href="https://github.com/luolongfei/ColabOne/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=luolongfei/ColabOne" alt="ColabOne Contributors" />
</a>

## 🎉 감사 인사

[**Alma**](https://alma.now/)와 [**Antigravity-Manager**](https://github.com/lbjlaq/Antigravity-Manager)에 특별한 감사를 전합니다 — ColabOne의 UI 디자인과 시각적 완성도, 그리고 앱 로고에 이르기까지 두 프로젝트로부터 직접적인 영감과 참고를 얻었습니다.

아래 오픈소스 프로젝트들에도 깊이 감사드립니다. 이들의 오픈소스 공개와 든든한 기술 기반이 있었기에 지금의 ColabOne이 탄생할 수 있었습니다(자세한 내용은 [NOTICE](NOTICE) 및 [third_party/SOURCES.md](third_party/SOURCES.md) 참고):

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)** *(Apache-2.0)* — Google Colab GPU 할당, 런타임 준비 확인 및 하트비트 수명 주기 관리 설계에 참고했습니다.
- **[EasyColab](https://github.com/wy51ai/easycolab)** *(Apache-2.0)* — 데스크톱-Colab 제어 아키텍처와 헤드리스 `rclone` Google 드라이브 마운트 패턴을 참고했습니다.
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)** *(MIT)* — 모델 매니페스트, 고정 버전 `llama.cpp` CUDA 배포 및 병렬 범위 다운로드 전략을 참고했습니다.
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)** *(MIT)* 및 **[ComfyUI](https://github.com/Comfy-Org/ComfyUI)** (`v0.46.0`, GPL-3.0) — 원격 GGUF LLM 추론 엔진과 노드 기반 이미지·비디오·멀티모달 워크플로 엔진으로 통합되어 있습니다.
- **데스크톱 및 프론트엔드 UI 스택** — **[MyGo](https://github.com/egoist/mygo)** (`v0.10.0`, MIT), **[coder/websocket](https://github.com/coder/websocket)** *(ISC)*, **[Vue 3](https://github.com/vuejs/core)**, **[Vite](https://github.com/vitejs/vite)**, **[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**, **[Phosphor Icons](https://github.com/phosphor-icons/vue)**, **[Shiki](https://github.com/shikijs/shiki)**, **[flag-icons](https://github.com/lipis/flag-icons)** *(MIT)* 및 **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)** *(SIL OFL 1.1)*.
- **원격 서비스, 터널 및 드라이브 스택** — **[FastAPI](https://github.com/fastapi/fastapi)** *(MIT)*, **[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)** *(Apache-2.0)*, **[cloudflared](https://github.com/cloudflare/cloudflared)** *(Apache-2.0)* 및 **[rclone](https://github.com/rclone/rclone)** *(MIT)*.

## 📜 라이선스

ColabOne은 원격 연동되는 [ComfyUI](https://github.com/Comfy-Org/ComfyUI) (`v0.46.0`, GPL-3.0)에 맞추어 **GNU General Public License v3.0** ([LICENSE](LICENSE)) 라이선스로 배포됩니다. 모든 업스트림 라이선스 및 저작권 고지는 [NOTICE](NOTICE)와 [third_party/SOURCES.md](third_party/SOURCES.md)에 보존되어 있습니다.
