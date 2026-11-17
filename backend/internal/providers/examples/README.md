# Go 공급처 실행 예제

설치·인증·실제 응답까지 실행하는 예제입니다. 공급처 코드와 인증·설치 도구는 Go이며 Freeway 백엔드 서버·Node·POC 게이트웨이를 시작하지 않습니다. 공식 OpenCode 자체 서버는 해당 공급처의 프로토콜에 필요한 경우에만 Go가 실행·정리합니다.

## 1. 설치부터 실제 응답까지

체크아웃한 저장소 루트에서 실행합니다. 소스 빌드에는 [Go 1.26 이상](https://go.dev/doc/install/)이 필요합니다. 먼저 go version을 확인합니다.

```sh
git clone https://github.com/wooinwoo/freeway-cli.git
cd freeway-cli
go version
bash scripts/examples/providers.sh
```

Windows PowerShell:

```powershell
go version
powershell -NoProfile -File scripts/examples/providers.ps1
```

[Bash 코드](../../../../scripts/examples/providers.sh) · [PowerShell 코드](../../../../scripts/examples/providers.ps1)

실행 순서는 Go 공급처 도구 빌드 → OpenCode·Cline 공식 네이티브 설치 → Kilo 익명 인증 구성 → 모델·추론 값 조회 → 한도 관측 → reflection/beam-free 실제 응답 검사입니다. 응답이 FREEWAY_PROVIDER_OK와 정확히 같아야 성공합니다. 실패한 단계에서 종료하고 대체 업체·유료 모델로 바꾸지 않습니다.

설치 경로는 기본적으로 사용자 홈의 .freeway/providers입니다. 다른 폴더는 FREEWAY_PROVIDER_HOME으로 지정합니다. 실행 정책으로 신뢰한 소스가 차단되면 현재 프로세스에만 -ExecutionPolicy Bypass를 적용할 수 있습니다. 시스템 정책을 변경하지 않습니다. [설치 안내](../../../../scripts/install/README.md)를 참고합니다.

## 2. 인증·모델·추론 깊이

설치한 명령을 사용합니다. API 키는 숨김 입력으로 받습니다.

```sh
provider_cli="$HOME/.freeway/providers/bin/freeway-providers"
"$provider_cli" auth all
"$provider_cli" auth groq
"$provider_cli" auth groq --confirm-free
"$provider_cli" auth opencode --login
"$provider_cli" auth cline --login
"$provider_cli" models kilo
"$provider_cli" probe kilo --model reflection/beam-free --reasoning-effort high
"$provider_cli" ask kilo --model reflection/beam-free --reasoning-effort low -- "한 문장으로 인사해 주세요."
```

--confirm-free는 해당 계정의 무료 조건을 확인한 경우에만 사용합니다. 새 키는 기존 확인을 초기화합니다. 이미 업체별 환경변수에 키를 준비했다면 auth all --from-env, 서비스가 stdin으로 전달하면 auth 업체ID --stdin을 사용합니다.

공식 CLI 인증의 브라우저·기기 확인은 공식 화면에서 완료합니다. 이미 로그인했다면 --login 없이 기존 로그인을 재사용합니다. CLI 도구의 API 키는 auth 하위 폴더에 암호화해 저장하며 기존 POC 저장소는 변경하지 않습니다.

PowerShell:

```powershell
$providerCli = "$HOME/.freeway/providers/bin/freeway-providers.exe"
& $providerCli auth all
& $providerCli auth cline --login
& $providerCli models kilo
& $providerCli ask kilo --model reflection/beam-free --reasoning-effort high "--" "한 문장으로 인사해 주세요."
```

모델 목록에는 각 ID·reasoning.efforts·select/probe/ask 실행 argv와 추론 값별 명령이 있습니다. 지원 목록에서 선택하며 Cline의 추론 깊이는 현재 생략해야 합니다.

## 3. Go 백엔드에서 가져다 쓰기

아래 프로그램은 HTTP 서버를 만들지 않고 SDK를 직접 호출합니다. 각각 독립된 main.go이며 backend 모듈 내부에서 실행·복사합니다.

| 예제 | 내용 |
| --- | --- |
| [authenticate](authenticate/main.go) | API 키 구성·무료 계정 확인·기존 공식 로그인·명시적 새 로그인 |
| [inspect](inspect/main.go) | 업체 연결 방식·무료 모델·추론 값·한도 관측 |
| [complete](complete/main.go) | 고정 업체·모델·추론 값·대화 이력·전체 JSON 결과·오류 |
| [basic](basic/main.go) | 인증·모델 선택·한도 관측·스트리밍·취소 |

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

complete의 기본 대화는 이름이 Freeway라는 이력을 전달한 뒤 이름을 묻습니다. basic은 확인 표식의 답변을 stdout으로, 한도 관측을 stderr로 출력합니다. 텍스트를 받았어도 최종 오류를 확인해야 합니다. Ctrl+C로 취소합니다.

예제는 독립 도구와 같은 선택형 파일 CredentialStore를 명시적으로 사용합니다. 실제 백엔드는 공통 비밀 저장소를 Load·Save 인터페이스로 연결하면 됩니다. 기본 Client는 메모리만 사용합니다. 로컬 사용량 원장·HTTP 서버·세션 DB는 이 예제에 없습니다.

| 환경변수 | 역할 |
| --- | --- |
| FREEWAY_PROVIDER | 기본 kilo. 호출 업체 |
| FREEWAY_MODEL | complete 필수. basic은 생략하면 첫 Ready 모델 |
| FREEWAY_REASONING_EFFORT | 모델이 지원하는 추론 값. 생략하면 업체 기본값 |
| FREEWAY_PROMPT | complete·basic의 마지막 사용자 질문 |
| FREEWAY_PROVIDER_HOME | 사용자 지정 도구·CLI·독립 인증 저장소 위치 |
| OPENCODE_BIN·CLINE_BIN | 공식 네이티브 실행 파일 경로 |
| FREEWAY_API_KEY | authenticate에서만 읽어 API 키로 전달 |
| FREEWAY_CONFIRM_FREE | authenticate에서 true일 때만 무료 조건 확인 |
| FREEWAY_LOGIN | authenticate에서 true이면 공식 대화형 로그인 실행 |

NodePath·BridgePath·FREEWAY_URL·FREEWAY_PROVIDER_BRIDGE는 사용하지 않습니다.

## 한도·실패 처리

quota 명령과 Go Quota는 추론 없이 구성 상태·현재 Client의 마지막 정규화된 실패를 반환합니다. providerRemaining은 미제공이면 null, authoritative는 false입니다. 로컬 잔여량·사용량 예약·정산은 백엔드가 담당합니다.

complete는 공급처 오류의 code·status·retryAfter를 출력하고 실패로 종료합니다. 없는 모델·유료 모델·미지원 깊이를 다른 경로로 바꾸지 않습니다. 사용량이 없으면 비용 0으로 기록하지 않습니다.

## 검증

```sh
go -C backend test -race ./...
FREEWAY_LIVE=1 go -C backend test -v -count=1 -run "^TestNativeLive$" ./internal/providers
```

기본 테스트는 로컬 fixture입니다. 두 번째 명령은 Kilo·OpenCode·Cline의 현재 무료 모델을 실제 호출하므로 설치·기존 로그인·무료 사용량이 필요합니다. FREEWAY_LIVE_PROVIDER와 FREEWAY_LIVE_MODEL로 범위를 좁힐 수 있습니다.

2026-11-17 Go 직결 시험에서는 Kilo 13개 중 12개, OpenCode 12개 중 11개, Cline 3개가 응답했습니다. Kilo stealth/glyph-cluster는 업체 HTTP 503, OpenCode nemotron-3.5-super-vl-free는 무료 제한 429를 반환했습니다. Kilo Beam low/high와 OpenCode DeepSeek low/max도 확인했습니다. 업체의 현재 가용성을 보장하는 수치가 아닙니다.

Node·npm을 실패시키는 실행 파일을 PATH 앞에 둔 상태에서 위 Go 직결 시험과 빈 폴더의 설치·인증·조회·Kilo probe가 완료됐고 해당 실행 파일 호출 흔적은 없었습니다.

새 계정의 브라우저 인증·키 없는 API 8곳의 추론은 별도 검증 대상입니다.

Windows x64에서도 Go 도구가 빈 폴더에 공식 CLI 두 개를 설치하고 Kilo 인증·모델 조회·high 실제 응답·check를 통과했습니다. Go SDK의 Windows 테스트와 인증 파일 동시 읽기·저장 회귀도 실행했습니다. Windows 소스 빌드는 Go 컴파일러 설치가 필요하며, 설치 스크립트의 정상 stderr·실패 처리는 네이티브 fixture로 별도 검사했습니다. macOS 실행과 Windows의 Cline 실제 로그인·추론은 미검증입니다.
