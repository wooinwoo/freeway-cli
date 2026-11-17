# 공급처 인증·연결·모델 시험

11개 공급처를 사용하는 Go 패키지와 실행 가능한 인증·모델·한도·호출 명령을 이 폴더에 모았습니다. `client.go`는 백엔드에서 import하는 실제 코드이며, `bridge.mjs`를 통해 기존 Node.js POC의 공급처 구현과 같은 게이트웨이·비밀 저장소·한도 원장을 재사용합니다. 독립적인 Go 공급처 재구현이나 최종 `freewayd` 서버가 완성됐다는 뜻은 아닙니다. 각 업체의 `auth.json`은 공개 인증 안내이며 비밀 값은 포함하지 않습니다.

백엔드는 [Go 패키지 사용법](INTEGRATION.md#go-공급처-패키지-사용)과 [실행 가능한 Go 예제](examples/basic/main.go)를 사용합니다. 아래에는 같은 기능의 CLI 명령을 정리했습니다.

## 코드 구성

| 파일 | 백엔드에서 사용하는 기능 |
| --- | --- |
| [client.go](client.go) | `New`, `Providers`, `Authenticate`, `Login`, `Models`, `Quota`, `Complete`, `Stream` |
| [bridge.mjs](bridge.mjs) | Go의 stdin JSON 요청을 기존 어댑터에 연결하고 stdout JSON Lines로 결과·텍스트·정규화 오류 반환 |
| [manage.mjs](manage.mjs) | 터미널 인증·모델 변경·추론 설정·한도 조회·실제 응답 검사 |
| [examples/basic/main.go](examples/basic/main.go) | 연결 확인, 모델 조회, 한도 확인, 모델·추론 깊이 지정, 스트림 호출까지 실행 |
| [client_test.go](client_test.go), [bridge.test.mjs](bridge.test.mjs) | 실제 Go→Node→로컬 HTTP 연결, 키 보호, 추론 옵션 전달, 한도, 실패와 취소 검사 |

Node.js 22 이상과 저장소의 `POC/`가 필요합니다. 아래 명령은 저장소 루트에서 실행하며 Bash와 PowerShell에서 같습니다. `list`를 제외한 명령은 기존 POC 서버를 시작하거나 재사용합니다. `FREEWAY_URL`과 `FREEWAY_DATA_DIR`은 기존 POC와 같습니다. 사용자 데이터는 Git에 포함하지 않습니다.

## 바로 실행

```sh
node backend/internal/providers/manage.mjs list
node backend/internal/providers/manage.mjs auth all
node backend/internal/providers/manage.mjs status all
node backend/internal/providers/manage.mjs quota all
node backend/internal/providers/manage.mjs models all
node backend/internal/providers/manage.mjs probe all
```

`auth all`은 API 키를 숨김 입력으로 받고, 이미 저장된 키는 재사용합니다. 키 입력에서 Enter를 누르면 해당 업체를 건너뜁니다. Cline 로그인이 필요하면 대화형 터미널에서 공식 인증을 실행합니다. 가입·키 발급·브라우저 인증은 공식 계정 화면에서 완료해야 하며 스크립트가 대신 승인하거나 계정을 만들지 않습니다. OpenCode·Cline은 먼저 공식 CLI를 설치합니다.

환경변수가 이미 준비된 환경에서는 다음 명령으로 키를 일괄 가져옵니다. 키를 명령행 인수로 전달하는 옵션은 없습니다. 키는 POC 관리 API를 통해 기존 암호화 저장소에 저장되므로, 이미 실행 중인 서버에도 적용됩니다.

```sh
node backend/internal/providers/manage.mjs auth all --from-env
```

NVIDIA·Vikasit·Groq·Gemini API·Novita는 공통 게이트웨이의 무료 계정 확인이 필요합니다. 실제 계정의 조건을 확인한 뒤 해당 업체에 `auth 업체ID --confirm-free`를 실행합니다. 확인 전에는 이 도구뿐 아니라 기존 POC의 자동 선택에서도 제외합니다. 이 플래그는 계정 요금을 조회하거나 유료 플랜을 무료로 바꾸지 않습니다. 새 API 키를 전달하면 이전 계정의 무료 확인은 초기화하며, 새 계정의 조건을 다시 확인해야 합니다.

## 업체별 안내

| ID | 연결 | 인증 입력 | 안내 |
| --- | --- | --- | --- |
| `kilo` | API | 익명 무료 또는 `KILO_API_KEY` | [Kilo](kilo/README.md) |
| `opencode` | 공식 CLI 서버 | 무료 풀 또는 공식 로그인 | [OpenCode](opencode/README.md) |
| `cline` | 공식 CLI ACP | `cline auth cline` | [Cline](cline/README.md) |
| `nvidia` | API | `NVIDIA_API_KEY` | [NVIDIA NIM](nvidia/README.md) |
| `zai` | API | `ZAI_API_KEY` | [Z.ai](zai/README.md) |
| `requesty` | API | `REQUESTY_API_KEY` | [Requesty](requesty/README.md) |
| `vikasit` | API | `VIKASIT_API_KEY` | [Vikasit](vikasit/README.md) |
| `openrouter` | API | `OPENROUTER_API_KEY` | [OpenRouter](openrouter/README.md) |
| `groq` | API | `GROQ_API_KEY` | [Groq](groq/README.md) |
| `gemini_api` | API | `GEMINI_API_KEY` | [Gemini API](gemini_api/README.md) |
| `novita` | API | `NOVITA_API_KEY` | [Novita](novita/README.md) |

## 명령과 모델 변경

| 명령 | 동작 |
| --- | --- |
| `list` | 공급처 ID·API 주소·인증 안내·환경변수·무료 판정·실행 argv를 JSON으로 출력합니다. 서버를 시작하지 않습니다. |
| `auth 업체ID` | API 키를 구성하거나 공식 CLI를 연결하고 무료 모델을 동기화합니다. 실제 추론 성공을 뜻하지 않습니다. |
| `auth 업체ID --from-env` | 해당 공급처의 환경변수에서 API 키를 가져옵니다. |
| `auth 업체ID --stdin` | 파이프로 전달한 키 한 개를 읽습니다. `all`에는 사용할 수 없습니다. |
| `auth cline --login` | 공식 `cline auth cline`을 실행하고 연결합니다. |
| `auth opencode --login` | 공식 `opencode auth login`을 실행하고 자체 무료 풀을 연결합니다. |
| `status 업체ID\|all` | 비밀 값 없이 연결·키 존재 여부·무료 계정 확인·모델 수를 출력합니다. |
| `quota 업체ID\|all` | 추론 없이 로컬 요청·토큰 집계·잔여 상한·마지막 실패를 조회합니다. 업체의 실제 잔여 한도는 모르면 `null`입니다. |
| `models 업체ID\|all` | 활성 공급처의 모델을 동기화하고 무료 모델 ID·준비 상태·출처·모델별 명령 argv를 출력합니다. |
| `select 업체ID 모델ID` | 해당 업체의 등록된 무료 모델을 POC 기본값으로 저장합니다. 출력 길이와 온도는 유지합니다. |
| `probe 업체ID\|all` | 업체별로 모델 하나를 호출하고 실제 답변이 `FREEWAY_PROVIDER_OK`인지 검사합니다. |
| `ask 업체ID -- "질문"` | 저장된 모델이 해당 업체에 있으면 사용하고, 없으면 준비된 무료 모델 하나를 사용합니다. |
| `ask 업체ID --model 모델ID --reasoning-effort 값 -- "질문"` | 해당 모델이 공개한 추론 깊이를 지정합니다. 미지원 값은 호출 전에 거절합니다. `probe`에도 같은 옵션을 사용할 수 있습니다. |

`select`에는 `models`가 반환한 전체 경로 ID 또는 `upstreamId`를 사용합니다. `probe`와 `ask`의 `--model`은 한 번의 요청에만 적용합니다. 업체 시험에는 모델 그룹이나 `free/auto`를 입력하지 않으며 다른 업체로 자동 전환하지 않습니다. 각 모델의 `commands`는 셸 문자열 대신 argv 배열이므로 백엔드는 셸을 거치지 않고 실행할 수 있습니다.

```sh
node backend/internal/providers/manage.mjs models kilo
node backend/internal/providers/manage.mjs select kilo cohere/north-mini-code:free
node backend/internal/providers/manage.mjs ask kilo -- "한 문장으로 인사해 주세요."
node backend/internal/providers/manage.mjs probe kilo --model kilo-auto/free
node backend/internal/providers/manage.mjs probe cline --timeout 60 --max-tokens 512
```

모델 ID는 공급처에서 바뀔 수 있습니다. 예제를 실행하기 전에 현재 목록을 확인합니다. `select`는 POC의 다음 기본 요청에 적용되며 실행 중인 Go TUI의 선택을 원격으로 바꾸지는 않습니다. CLI 공급처의 출력 상한은 공식 CLI가 결정하므로 `--max-tokens`만으로 강제되지 않습니다.

## 모델·추론 깊이·한도 사용

```sh
node backend/internal/providers/manage.mjs models kilo
node backend/internal/providers/manage.mjs ask kilo --model reflection/beam-free --reasoning-effort high -- "한 문장으로 인사해 주세요."
node backend/internal/providers/manage.mjs ask opencode --model deepseek-v4.1-flash-free --reasoning-effort low -- "한 문장으로 인사해 주세요."
node backend/internal/providers/manage.mjs quota kilo
node backend/internal/providers/manage.mjs quota cline
```

`models`의 각 모델에 `reasoning.parameter`와 `reasoning.efforts`가 있습니다. 고정 enum을 추측하지 않고 반환된 값을 사용합니다. Kilo는 모델별 `reasoning` 또는 `reasoning_effort`, OpenCode는 공식 세션의 `variant`로 전달합니다. Cline ACP는 현재 이 브리지에서 추론 깊이를 제어하지 않으므로 목록이 비어 있고 지정하면 `unsupported_reasoning`으로 실패합니다. 지정한 값을 조용히 무시하지 않습니다. `max_tokens`는 답변 길이 상한이며 추론 깊이와 다릅니다.

`quota`의 `localRemaining`은 로컬 설정상 남은 요청 수, `localTokenRemaining`은 관측·추정 토큰을 반영한 로컬 토큰 상한입니다. 해당 제한을 모르면 항목을 만들지 않습니다. `providerRemaining: null`, `authoritative: false`는 업체가 실제 잔여량을 제공하지 않았다는 뜻입니다. 이를 무제한이나 잔여 0으로 해석하지 않습니다. 다른 앱·PC·같은 IP에서 발생한 사용량은 이 집계에 포함되지 않습니다. 오류 코드·`ready`·`reason`도 함께 확인합니다.

## 백엔드가 사용할 경계

- `list`는 현재 POC의 단일 공급처 정의를 재사용하므로 API 기본 주소·환경변수·무료 판정 규칙을 별도로 복제하지 않습니다. 현재 설정은 [catalog.mjs](../../../POC/src/catalog.mjs), HTTP 호출은 [adapters.mjs](../../../POC/src/adapters.mjs)에 있습니다.
- 공급처별 `auth.json`에는 공개 인증 URL·공식 문서·로그인 argv·주의 조건만 둡니다. 키·토큰은 공통 비밀 저장 계층에 맡깁니다. 업체별 문서는 모델 조회·호출·취소·프로토콜 순서와 원본 코드 위치를 설명합니다.
- JSON stdout은 기계가 읽는 결과입니다. 안내와 공식 로그인 화면은 stderr로 보냅니다. `ok: false`이면 실행 파일 종료 코드는 1입니다. 배치 실행은 실패한 업체를 기록하고 나머지 업체를 계속 검사합니다.
- `configured`, `listed`, `ready`는 실제 추론 성공과 다릅니다. 허용 목록만 등록하는 공급처는 키가 잘못돼도 모델 목록을 보여 줄 수 있습니다. `probe`의 `passed`와 `inferenceVerified: true`를 실제 응답 근거로 사용합니다.
- `needs_key`, `needs_login`, `needs_free_confirmation`, `quota_exhausted`, `unexpected_response`, `failed`를 구분합니다. 오류에는 공통 코드와 확인 가능한 HTTP 상태만 남깁니다. 키나 공급처의 원문 오류는 출력하지 않습니다.
- 최종 Go 구현에서 인터페이스·라우팅·한도 원장을 새로 정할 때는 [기존 책임 경계](../../../docs/development/architecture.md)를 따릅니다. 이 도구의 임시 POC 관리 API를 최종 API 계약으로 고정하지 않습니다.

## 검증

먼저 기존 POC 테스트 8개를 통과했고, 이 도구의 테스트를 실패 상태부터 추가한 뒤 구현했습니다. 명령 테스트는 실제 loopback HTTP 서버와 가짜 공급처를 사용하며 키 암호화·표준입력·배치 실패·모델 변경·업체 고정·401/403/429·잘못된 답변을 검사합니다. 실제 키나 유료 추론을 요구하지 않습니다.

```sh
node --test backend/internal/providers/manage.test.mjs backend/internal/providers/bridge.test.mjs POC/tests/*.test.mjs
```

2026-11-17에는 별도 임시 데이터 디렉터리와 임의 loopback 포트로 실제 호출도 실행했습니다. 기존 사용자 설정과 키 저장소는 변경하지 않았습니다.

| 공급처 | 이번 실행 결과 |
| --- | --- |
| Kilo | 공통 HTTP 어댑터로 후보 17개 전수 호출. 무료 13개 모두 확인 응답 수신, 각각 보고 비용 0. 나머지 4개는 명시적 유료 모델로 인증 단계에서 거절됐고 무료 필터를 수정해 제외했습니다. [전체 ID·근거](kilo/README.md#전체-모델-실제-호출-결과). |
| OpenCode | 수정한 브리지로 무료 13개 전수 호출. 12개 확인 응답·보고 비용 0. `iceman-free`는 상위 서버의 배포 설정 오류 HTTP 400으로 실패했습니다. DeepSeek·MiMo는 전체 Freeway 게이트웨이에서도 응답·모델 변경을 확인했고, 강제 읽기 권한 거절도 검증했습니다. |
| Cline | 기존 공식 로그인과 ACP 브리지로 현재 무료 3개 전부 확인 응답·정상 종료. 비용 수치는 브리지가 반환하지 않습니다. [전체 ID·근거](cline/README.md#전체-모델-실제-호출-결과). |
| 나머지 API 8곳 | 키가 없어 `needs_key`. 인증·추론 성공으로 계산하지 않음. |

이어서 `select`로 Kilo를 `cohere/north-mini-code:free`, Cline을 `cline-free/solar-mini4`로 바꾸고, 모델을 다시 지정하지 않은 `probe`에서도 선택한 각 모델의 확인 응답을 받았습니다. Kilo는 이 호출도 비용 0을 보고했습니다.

OpenCode 결과 정정: 같은 WSL 실행 파일 1.18.35에서 기본 TUI·CLI는 성공했고, `permission: {"*":"deny"}`만 추가하면 무료 제한 오류가 나타났습니다. 전체 권한 거부를 제거한 다음 요청은 다시 성공했습니다. 이를 실제 할당량 소진이나 버전 문제로 단정하면 안 됩니다. 수정본은 기본적으로 모든 도구를 거부하되 읽기 스키마만 승인 대기로 남기고 실제 권한 요청은 거절합니다. 최종 게이트웨이 시험에서 DeepSeek·MiMo 응답, 모델 변경, 권한 거절과 파일 내용 미노출을 확인했습니다. 상세 근거는 [OpenCode 안내](opencode/README.md#연결-설정-비교-결과)를 따릅니다.

위 결과는 Linux/WSL에서 실행한 관측입니다. Windows의 실제 인증·호출과 모든 모델·계정의 현재 가용성을 검증한 것은 아닙니다. 기본 인증 저장과 CLI 프로토콜의 플랫폼별 차이는 각각 기존 POC와 공식 CLI의 지원 범위를 따릅니다.

추가로 실제 Go 실행 예제로 Kilo `reflection/beam-free`의 `low`·`high`, OpenCode `deepseek-v4.1-flash-free`의 `low`·`max`, Cline 기본 추론의 확인 응답을 받았습니다. 외부 HTTP 요청의 `reasoning.effort`와 OpenCode 세션의 `variant`에 각각 지정한 값이 들어갔습니다. Cline에 `high`를 지정하면 추론 전에 `unsupported_reasoning`으로 거절됐습니다. 이 시험은 새로운 임시 게이트웨이에서 실행해 기존 사용자 설정을 바꾸지 않았습니다.
