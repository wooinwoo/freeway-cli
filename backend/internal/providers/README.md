# 공급처 사용하기

백엔드에서 인증·모델 조회·추론 설정·한도 확인·호출을 사용하는 Go 패키지입니다. 검증된 Node 어댑터와 같은 로컬 게이트웨이·비밀 저장소·사용량 원장을 재사용합니다. 최종 Go HTTP 서버인 `freewayd`는 별도 구현 영역입니다.

- **처음 실행:** 아래 설치·인증 순서
- **백엔드 개발:** [Go 예제](examples/basic/main.go) · [API 명세](API.md)
- **다른 언어:** [브리지 JSON Schema](bridge.schema.json)
- **설치·업데이트 세부사항:** [설치기 안내](../../../scripts/install/README.md)

## 1. 실행 환경 설치

저장소 루트에서 실행합니다. Node가 없는 환경도 bootstrap이 준비합니다.

```sh
bash scripts/install/providers.sh install
```

Windows PowerShell:

```powershell
powershell -NoProfile -File scripts/install/providers.ps1 -Action install
```

HTTP API만 쓸 때는 `--api-only` 또는 `-ApiOnly`를 추가합니다. API 업체 9곳은 별도 업체 프로그램 대신 키를 연결합니다. OpenCode·Cline에는 공식 CLI를 설치합니다. 설치 폴더는 기본적으로 `~/.freeway/providers`입니다.

## 2. 인증·연결

Linux / WSL / macOS:

```sh
provider_cli="$HOME/.freeway/providers/bin/freeway-providers"
"$provider_cli" auth all
"$provider_cli" status all
```

Windows PowerShell:

```powershell
$providerCli = "$HOME/.freeway/providers/bin/freeway-providers.ps1"
& $providerCli auth all
& $providerCli status all
```

API 키는 숨김 입력으로 받으며 Enter로 해당 업체를 건너뜁니다. 이미 로그인한 공식 CLI는 재사용합니다. 필요한 기기·브라우저 인증은 공식 화면에서 완료합니다. 특정 CLI의 인증을 직접 시작하려면 `auth cline --login` 또는 `auth opencode --login`을 사용합니다.

키를 환경변수에 준비했다면 `auth all --from-env`, 백엔드가 stdin으로 전달한다면 `auth 업체ID --stdin`을 사용합니다. 키를 명령행 인수에 넣지 않습니다. 일부 업체에 키가 없으면 배치의 종료 코드는 1이며 업체별 `results`에 성공·실패가 함께 기록됩니다.

NVIDIA·Vikasit·Groq·Gemini API·Novita는 계정의 무료 조건을 확인한 뒤 `auth 업체ID --confirm-free`를 실행합니다. 새 키를 저장하면 기존 확인은 초기화됩니다. 이 설정은 요금제·잔액 조회나 추론 성공을 대신하지 않습니다.

## 3. 모델·추론 깊이·한도·호출

아래는 Bash 예시입니다. PowerShell에서는 `& $providerCli`로 실행하고 질문 구분자를 `"--"`로 인용합니다.

```sh
"$provider_cli" models kilo
"$provider_cli" ask kilo --model reflection/beam-free --reasoning-effort high -- "인사해 주세요."
"$provider_cli" probe opencode --model deepseek-v4.1-flash-free --reasoning-effort low
"$provider_cli" quota all
```

| 작업 | 사용할 명령·필드 |
| --- | --- |
| 모델 목록 | `models 업체ID`: ID·준비 상태·지원 추론 값·실행 argv 반환 |
| 한 요청의 모델 변경 | `ask`·`probe`의 `--model`. 업체·모델을 고정하며 다른 경로로 자동 전환하지 않음 |
| 공유 기본 모델 저장 | `select 업체ID 모델ID`. 대화별 모델 선택에는 사용하지 않음 |
| 추론 깊이 | `--reasoning-effort 값`. 모델의 `reasoning.efforts`에 있는 값만 허용 |
| 한도 확인 | `quota 업체ID\|all`. 추론 요청 없이 로컬 집계·준비 상태·마지막 실패 조회 |
| 실제 응답 점검 | `probe 업체ID`. 답변이 `FREEWAY_PROVIDER_OK`와 일치해야 통과 |

모델·추론 옵션은 바뀔 수 있으므로 먼저 목록을 조회합니다. Kilo는 모델별 `reasoning` 또는 `reasoning_effort`, OpenCode는 공식 `variant`로 전달합니다. Cline ACP의 추론 깊이는 현재 미지원이며 지정하면 `unsupported_reasoning`으로 거절합니다. `max_tokens`는 추론 깊이가 아닌 출력 상한이며 CLI 공급처에는 같은 상한을 강제하지 못합니다.

한도의 `localRemaining`은 로컬 요청 상한, `localTokenRemaining`은 관측·추정 토큰을 반영한 로컬 토큰 상한입니다. 업체의 실제 잔여량을 모르면 `providerRemaining: null`, `authoritative: false`입니다. 이를 잔여 0이나 무제한으로 표시하지 않습니다. 다른 앱·PC·같은 IP의 사용량은 포함되지 않습니다.

## Go에서 사용

`github.com/wooinwoo/freeway-cli/backend/internal/providers`를 import합니다. 실제 연결 순서는 [실행 가능한 Go 예제](examples/basic/main.go)에 있습니다.

| 함수 | 역할 |
| --- | --- |
| `New(Config)` | 실행 환경·로컬 게이트웨이 주소 설정 |
| `Providers(ctx)` | 공급처·연결 방식 조회 |
| `Authenticate(ctx, id, options)` | 키 구성·기존 로그인 연결 |
| `Login(ctx, id, stdin, stdout, stderr)` | 터미널에서 공식 CLI 인증 시작 |
| `Models(ctx, id)` | 무료 모델·추론 옵션 조회 |
| `Quota(ctx, id)` | 로컬 한도 관측 조회 |
| `Complete(ctx, Request)` | 완료된 답변 반환 |
| `Stream(ctx, Request, emit)` | 텍스트 콜백과 최종 결과 반환 |

`Request.Model`로 대화의 모델을 바꾸고 `Request.ReasoningEffort`로 깊이를 설정합니다. NodePath를 생략하면 전용 설치의 Node를 먼저 사용합니다. BridgePath에는 이 폴더의 `bridge.mjs` 절대 경로를 넣습니다. Go API는 실행 중인 게이트웨이를 사용하며 앱 계층이 서버의 시작·종료를 소유합니다. 위 CLI 인증 명령은 필요하면 기존 POC 게이트웨이를 시작합니다.

저장소 루트에서 Go 예제 실행:

```sh
FREEWAY_MODEL=reflection/beam-free FREEWAY_REASONING_EFFORT=high go -C backend run ./internal/providers/examples/basic
```

`Ready`·목록 조회·인증 구성은 실제 응답 성공과 다릅니다. 최종 `err`를 확인하고, 일부 스트림을 받았어도 오류가 있으면 실패로 처리합니다. 사용량이 없으면 비용 0으로 저장하지 않습니다. 필드·기본값·오류·취소·프로토콜의 정확한 계약은 [API 명세](API.md)를 따릅니다.

## 업체별 안내

`openai_compatible`은 HTTP 형식이며 OpenAI 업체·계정을 뜻하지 않습니다. 새 코드는 `transport`, `protocol`, `requiresCLI`로 연결 방식을 구분합니다.

| ID | 연결 | 인증·프로토콜·모델 검증 |
| --- | --- | --- |
| `kilo` | HTTP API | [Kilo](kilo/README.md) |
| `nvidia` | HTTP API | [NVIDIA NIM](nvidia/README.md) |
| `zai` | HTTP API | [Z.ai](zai/README.md) |
| `requesty` | HTTP API | [Requesty](requesty/README.md) |
| `vikasit` | HTTP API | [Vikasit](vikasit/README.md) |
| `openrouter` | HTTP API | [OpenRouter](openrouter/README.md) |
| `groq` | HTTP API | [Groq](groq/README.md) |
| `gemini_api` | HTTP API | [Gemini API](gemini_api/README.md) |
| `novita` | HTTP API | [Novita](novita/README.md) |
| `opencode` | 공식 CLI 서버 | [OpenCode](opencode/README.md) |
| `cline` | 공식 CLI ACP | [Cline](cline/README.md) |

각 `auth.json`은 공개 인증 안내이며 키·토큰 저장 파일이 아닙니다.

## 코드 위치

| 파일 | 책임 |
| --- | --- |
| [client.go](client.go) · [types.go](types.go) | Go 호출·스트림·공개 데이터 타입 |
| [runtime.go](runtime.go) | 설치된 Node 발견 |
| [bridge.mjs](bridge.mjs) | Go와 Node 사이의 JSON 요청·이벤트 |
| [manage.mjs](manage.mjs) | 인증·모델·추론·한도 CLI |
| [catalog.mjs](../../../POC/src/catalog.mjs) | 공급처 정의·무료 판정·모델 정규화 |
| [adapters.mjs](../../../POC/src/adapters.mjs) | HTTP 공급처 모델 조회·추론 |
| [opencode.mjs](../../../POC/src/opencode.mjs) · [cline.mjs](../../../POC/src/cline.mjs) | 공식 CLI 프로토콜·권한 거절·취소 |

최종 Go 이식의 라우팅·쿼터·저장 책임은 [공통 구조](../../../docs/development/architecture.md#의존성과-책임)를 따릅니다. 공급처 어댑터마다 별도 fallback이나 사용량 원장을 만들지 않습니다.

## 검증

```sh
go -C backend test -race ./...
node --test backend/internal/providers/*.test.mjs POC/tests/*.test.mjs tests/install/*.test.mjs
```

실호출 기록은 업체별 README에 있습니다. Kilo 무료 13개·Cline 무료 3개는 전부 응답했고 OpenCode는 당시 무료 13개 중 12개 응답, `iceman-free`는 상위 배포 설정 오류였습니다. Go 예제로 Kilo low/high·OpenCode low/max도 확인했습니다. 이 수치는 현재 가용성을 보장하지 않습니다. 나머지 API 8곳은 키가 없어 실호출 미검증입니다.

설치·업데이트·진단과 새 설치 바이너리의 호출은 Linux/WSL에서 검증했습니다. Windows는 PowerShell 문법 검사·Go 크로스컴파일까지 확인했으며 실제 설치·인증과 macOS 실행은 미검증입니다.
