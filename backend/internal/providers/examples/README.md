# 실행 예제

설치부터 실제 응답까지 확인하는 CLI 예제와 백엔드에 복사해 쓸 Go 예제입니다. 모든 명령은 체크아웃한 저장소 루트에서 실행합니다. 실행은 무료 경로의 실제 사용량을 소비하며, 한도·모델 변경으로 실패할 수 있습니다.

## 1. 설치·인증부터 실제 응답까지

처음 받는 환경이라면 소스를 준비합니다.

```sh
git clone https://github.com/wooinwoo/freeway-cli.git
cd freeway-cli
```

Linux / WSL / macOS:

```sh
bash scripts/examples/providers.sh
```

Windows PowerShell:

```powershell
powershell -NoProfile -File scripts/examples/providers.ps1
```

신뢰한 체크아웃인데 실행 정책으로 차단되면 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/examples/providers.ps1`로 현재 프로세스에서만 실행할 수 있습니다. 특히 WSL의 UNC 경로는 네트워크 경로로 판정될 수 있습니다. 시스템 실행 정책은 변경하지 않으며 조직 정책은 우회하지 않습니다. [PowerShell 공식 설명](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_execution_policies?view=powershell-5.1)을 참고합니다.

[Bash 코드](../../../../scripts/examples/providers.sh) · [PowerShell 코드](../../../../scripts/examples/providers.ps1)

이 예제는 **전용 Node·OpenCode·Cline 설치 → Kilo 익명 인증 구성 → 모델·추론 옵션 조회 → 로컬 한도 조회 → 실제 모델 응답 검사** 순서로 실행합니다. 기본 모델은 `reflection/beam-free`이며 응답이 `FREEWAY_PROVIDER_OK`와 정확히 같아야 성공합니다. 실패한 단계에서 종료하고 다른 업체·유료 모델로 전환하지 않습니다.

이 CLI 예제에는 Go나 기존 Node 설치가 필요하지 않습니다. Bash 쪽은 curl·tar·SHA256 도구, Windows는 PowerShell 5.1 이상과 네트워크 연결이 필요합니다. 사용자 홈의 `.freeway/providers`에 설치하고, 필요하면 로컬 POC 게이트웨이를 시작합니다. 이미 게이트웨이가 있으면 재사용합니다. 설치 경로·지원 OS·업데이트는 [설치기 안내](../../../../scripts/install/README.md)를 따릅니다.

## 2. 다른 업체의 인증

설치 후 생성된 명령을 사용합니다. API 키는 숨김 입력으로 받습니다.

```sh
provider_cli="$HOME/.freeway/providers/bin/freeway-providers"
"$provider_cli" auth groq
"$provider_cli" auth groq --confirm-free
"$provider_cli" auth opencode --login
"$provider_cli" auth cline --login
```

`--confirm-free`는 본인 계정의 무료 조건을 확인한 경우에만 실행합니다. 계정·키 발급이나 브라우저·기기 인증은 공식 화면에서 완료해야 합니다. 기존 CLI 로그인이 있으면 `auth opencode`·`auth cline`으로 재사용합니다. 여러 업체를 한 번에 연결하려면 `auth all`을 사용합니다.

PowerShell에서는 다음처럼 실행합니다.

```powershell
$providerCli = "$HOME/.freeway/providers/bin/freeway-providers.ps1"
& $providerCli auth groq
& $providerCli auth groq --confirm-free
& $providerCli auth cline --login
```

새 키를 저장하면 이전 무료 계정 확인은 초기화됩니다. 인증 구성 성공은 추론 성공이 아니므로 `models 업체ID`로 ID·지원 추론 값을 확인하고 `probe 업체ID --model 모델ID`로 실제 응답을 확인합니다. 상세 조건은 [업체별 안내](../README.md#업체별-안내)를 따릅니다.

## 3. Go 백엔드 예제

Go 1.26 이상이 별도로 필요합니다. [공식 설치 안내](https://go.dev/doc/install/)에 따라 설치한 뒤 `go version`으로 확인합니다. 위 CLI 예제를 먼저 실행하면 공급처 실행 환경·Kilo 연결·로컬 게이트웨이가 준비됩니다. Go 예제가 게이트웨이를 새로 시작하지는 않습니다.

| 예제 | 코드에서 확인할 내용 |
| --- | --- |
| [authenticate](authenticate/main.go) | 기존 인증 재사용·환경변수의 키 전달·명시적인 무료 계정 확인·공식 CLI 로그인 |
| [inspect](inspect/main.go) | 공급처 연결 방식·모델 ID·지원 추론 값·로컬 한도 조회. 추론 요청 없음 |
| [complete](complete/main.go) | 모델을 고정한 일반 호출·대화 이력·추론 깊이·전체 결과·오류 코드와 RetryAfter |
| [basic](basic/main.go) | 인증·사용 가능한 모델 선택·한도 확인·스트리밍·취소 |

각 프로그램은 독립된 `main.go`이며 공통 예제 헬퍼 없이 `providers.New`부터 보여줍니다. Go `internal` 규칙에 따라 코드는 `backend/` 모듈 안에서 사용합니다.

```sh
go -C backend run ./internal/providers/examples/authenticate
go -C backend run ./internal/providers/examples/inspect
FREEWAY_MODEL=reflection/beam-free FREEWAY_REASONING_EFFORT=high go -C backend run ./internal/providers/examples/complete
FREEWAY_MODEL=reflection/beam-free FREEWAY_REASONING_EFFORT=low go -C backend run ./internal/providers/examples/basic
```

PowerShell:

```powershell
go -C backend run ./internal/providers/examples/authenticate
go -C backend run ./internal/providers/examples/inspect
$env:FREEWAY_MODEL = 'reflection/beam-free'
$env:FREEWAY_REASONING_EFFORT = 'high'
go -C backend run ./internal/providers/examples/complete
$env:FREEWAY_REASONING_EFFORT = 'low'
go -C backend run ./internal/providers/examples/basic
```

`complete`의 기본 대화는 이름이 Freeway라는 이전 대화를 전달한 뒤 이름을 묻습니다. 성공 시 `content`·모델·사용량 등 전체 결과를 JSON으로 출력합니다. `basic`은 기본 질문의 답변 텍스트를 stdout으로, 한도를 stderr로 출력합니다. 스트리밍 중 텍스트를 받았어도 마지막 오류를 확인해야 합니다. 두 예제 모두 Ctrl+C로 취소할 수 있습니다.

모델을 바꾸려면 다음 실행의 `FREEWAY_PROVIDER`·`FREEWAY_MODEL`을 바꿉니다. 추론 값은 `inspect`의 `reasoning.efforts`에서 고릅니다. Cline은 현재 추론 깊이를 지원하지 않으므로 `FREEWAY_REASONING_EFFORT`를 비웁니다.

| 환경변수 | 기본값·역할 |
| --- | --- |
| `FREEWAY_PROVIDER` | `kilo`. 사용할 업체 |
| `FREEWAY_MODEL` | `complete`에는 필수. `basic`은 생략하면 그 업체의 첫 Ready 모델 선택 |
| `FREEWAY_REASONING_EFFORT` | 생략하면 업체 기본값 |
| `FREEWAY_PROMPT` | `complete`·`basic`의 마지막 사용자 질문 |
| `FREEWAY_URL` | `http://127.0.0.1:4141`. 실행 중인 게이트웨이 주소 |
| `FREEWAY_PROVIDER_HOME` | 기본 설치 경로 외에 설치했다면 같은 절대 경로 지정. 실행 중 게이트웨이에도 적용해야 CLI 탐색에 반영됨 |
| `FREEWAY_PROVIDER_BRIDGE` | Go 예제의 bridge.mjs 절대 경로. 예제는 `go -C backend` 기준으로 기본 경로를 찾음 |
| `FREEWAY_API_KEY` | `authenticate`에서만 읽음. 생략하면 기존 인증 유지. 출력·명령행 인수에 넣지 않음 |
| `FREEWAY_CONFIRM_FREE` | `authenticate`에서 명시적으로 `true`인 경우에만 무료 계정 확인 |
| `FREEWAY_LOGIN` | `authenticate`에서 `true`이면 OpenCode·Cline의 공식 대화형 로그인 시작. API 키와 함께 사용 불가 |

이미 키를 안전하게 환경변수에 준비한 서비스는 `FREEWAY_API_KEY`를 통해 Go 인증 예제를 실행할 수 있습니다. 터미널에서 키를 넣는 작업은 위 숨김 입력 CLI를 권장합니다. CLI 로그인 예제는 `FREEWAY_PROVIDER=cline FREEWAY_LOGIN=true go -C backend run ./internal/providers/examples/authenticate`입니다. 웹 요청 처리기에서 대화형 로그인을 실행하지 않습니다.

## 오류·검증

`complete`는 공급처 오류의 `code`·`status`·`retryAfter`를 stderr로 출력하고 1로 종료합니다. context 취소·시간 초과도 실패입니다. 일반 호출은 모델을 지정하지 않으면 네트워크 요청 전에 실패합니다. 예제는 재시도·대체 업체 선택을 하지 않습니다.

한도 응답의 `providerRemaining: null`·`authoritative: false`는 업체의 실제 잔여량을 모른다는 뜻입니다. 사용량이 없으면 비용 0으로 간주하지 않습니다.

```sh
go -C backend test -race ./...
```

이 명령은 컴파일·자동 테스트입니다. 실제 계정·한도·응답까지 확인하려면 위 설치 예제와 Go 명령을 실행해야 합니다.

## 예제 검증 기록

2026-11-17, 사용자 기본 설치·사용량 저장소와 분리한 임시 설치 폴더·게이트웨이에서 확인했습니다. 모델 목록·계정·상위 한도가 바뀌면 같은 명령도 실패할 수 있습니다.

| 실행 환경 | 실제 확인한 내용 |
| --- | --- |
| Linux/WSL x64 | 빈 폴더에서 Bash 설치 예제 실행. Node 26.13.1·OpenCode 1.18.35·Cline 3.0.62 설치, Kilo 인증·조회·probe 성공 |
| Linux/WSL x64 | Kilo·OpenCode·Cline 각각 Go 인증·조회·일반 호출·스트리밍 성공. Kilo·OpenCode의 low/max 전송, Cline의 깊이 생략 확인 |
| Linux/WSL x64 | README의 Kilo `complete high`는 대화 이력의 이름 Freeway를 반환하고 `basic low`는 응답 표식 반환 |
| Windows x64 | 전용 Node·두 공식 CLI 실제 설치·버전 확인. Windows용으로 빌드한 네 Go 예제를 Windows Node·게이트웨이와 실행해 Kilo 인증·조회·complete high·stream low 성공 |
| Windows PowerShell 5.1 | PowerShell 설치 예제 전체 실행 성공. 인증·모델·한도 조회 후 Kilo 응답 표식을 검사하고 0으로 종료 |
| 실패 처리 | Cline high는 `unsupported_reasoning`, 없는 모델은 `model_not_found`, 모델 미지정과 API 키·CLI 로그인 동시 지정도 실패. 없는 모델 요청은 추론 사용량이 증가하지 않음 |
| PowerShell 5.1 회귀 | 로그 리다이렉션 중 정상 stderr가 있어도 완료하며 실제 native 종료 코드 0·7 보존 |

기존 CLI 환경을 재사용했습니다. 새 계정의 브라우저·기기 인증 완료, Windows의 OpenCode·Cline 추론, macOS와 키 없는 API 8곳의 추론은 검증하지 않았습니다.
