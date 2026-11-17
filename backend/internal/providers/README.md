# 공급처 사용하기

Go에서 업체 HTTP API와 공식 OpenCode·Cline CLI를 직접 사용하는 패키지입니다. 인증 구성·무료 모델 조회·모델과 추론 깊이 선택·스트리밍·취소·공급처 오류 관측을 제공합니다.

이 패키지는 백엔드 HTTP 서버·라우터·대화 DB·쿼터 원장을 만들지 않습니다. 백엔드는 CredentialStore를 주입하고 요청 선택·예약·정산·재시도 정책을 소유합니다. 실행에 Node·Freeway POC 게이트웨이는 필요하지 않습니다.

- 처음 실행: [설치부터 실제 응답까지 예제](examples/README.md)
- 백엔드 개발: [Go API 명세](API.md)
- 설치·업데이트: [설치기 안내](../../../scripts/install/README.md)

## 실행 환경 준비

체크아웃한 저장소 루트에서 실행합니다. 소스 빌드에는 [Go 1.26 이상](https://go.dev/doc/install/)이 필요합니다.

```sh
bash scripts/install/providers.sh install
```

Windows PowerShell:

```powershell
powershell -NoProfile -File scripts/install/providers.ps1 -Action install
```

설치기는 독립 Go 도구 freeway-providers를 빌드하고, 공식 네이티브 CLI 패키지를 직접 받아 무결성과 실행 버전을 확인합니다. Node·npm을 실행하지 않습니다. HTTP API 9곳은 업체 프로그램을 설치하지 않으므로 HTTP만 쓸 때는 --api-only 또는 -ApiOnly를 사용합니다.

## 인증·모델·추론·한도

```sh
provider_cli="$HOME/.freeway/providers/bin/freeway-providers"
"$provider_cli" auth kilo
"$provider_cli" auth cline --login
"$provider_cli" auth all --from-env
"$provider_cli" models kilo
"$provider_cli" select kilo reflection/beam-free
"$provider_cli" probe kilo --model reflection/beam-free --reasoning-effort high
"$provider_cli" ask kilo --model reflection/beam-free --reasoning-effort low -- "인사해 주세요."
"$provider_cli" quota all
"$provider_cli" check
"$provider_cli" update
```

Windows는 생성된 freeway-providers.exe 또는 freeway-providers.ps1을 호출합니다. PowerShell 5.1에서 질문 구분자는 "--"로 인용합니다.

select는 이 독립 도구의 기본 모델만 저장합니다. 백엔드의 대화별 모델은 Request.Model로 지정합니다.

API 키는 숨김 입력·업체별 환경변수·auth 업체ID --stdin으로 받습니다. 키를 명령행 인수에 넣지 않습니다. CLI 도구의 키는 전용 auth 폴더에 암호화해 저장합니다. 기존 공식 CLI 로그인은 재사용하며 새 기기·브라우저 인증은 공식 화면에서 완료합니다.

NVIDIA·Vikasit·Groq·Gemini API·Novita는 무료 계정 조건을 확인한 뒤 auth 업체ID --confirm-free를 실행합니다. 새 키를 저장하면 이전 확인은 초기화됩니다. auth all의 일부 업체가 준비되지 않으면 업체별 결과를 출력하고 1로 종료합니다.

models의 reasoning.efforts에 있는 값만 지정합니다. Kilo는 모델별 reasoning 또는 reasoning_effort, OpenCode는 variant로 전달합니다. Cline ACP는 현재 추론 깊이 미지원이며 지정하면 unsupported_reasoning으로 거절합니다.

Quota는 공급처의 마지막 오류와 구성 상태를 관측합니다. 정확한 잔여량을 제공받지 못하면 providerRemaining은 null, authoritative는 false입니다. 로컬 요청 수·토큰 수의 예약·정산·잔여량은 백엔드의 공통 원장이 담당합니다. 이 패키지에서 잔여량을 추정하거나 별도 원장을 만들지 않습니다.

## Go에서 사용

backend 모듈 내부에서 import합니다. Go internal 접근 규칙을 따릅니다.

```go
credentials, err := providers.NewFileCredentials(authDir)
if err != nil {
    return err
}
client, err := providers.New(providers.Config{Credentials: credentials})
if err != nil {
    return err
}
defer client.Close()

result, err := client.Complete(ctx, providers.Request{
    Provider: "kilo",
    Model: "reflection/beam-free",
    ReasoningEffort: "high",
    Messages: []providers.Message{{Role: "user", Content: "안녕하세요."}},
})
```

위 파일 저장소는 독립 도구·예제용 선택지입니다. 실제 백엔드는 자체 비밀 저장소를 CredentialStore의 Load·Save로 연결합니다. Config.Credentials를 생략하면 인증 구성은 클라이언트 메모리에만 유지됩니다. 모든 Go 예제는 [examples](examples/README.md)에 있습니다.

Client는 선택한 업체·모델만 호출합니다. 다른 업체 선택·자동 fallback·재시도는 하지 않습니다. Stream 콜백에서 텍스트를 받았어도 마지막 err를 확인해야 합니다. 사용량이 없으면 비용 0으로 기록하지 않습니다. Client.Close는 진행 중 작업과 자신이 실행한 공식 CLI만 정리합니다.

## 업체별 안내

openai_compatible은 HTTP 형식이며 OpenAI 업체·계정을 뜻하지 않습니다.

| ID | 연결 | 안내 |
| --- | --- | --- |
| kilo | HTTP API | [Kilo](kilo/README.md) |
| nvidia | HTTP API | [NVIDIA](nvidia/README.md) |
| zai | HTTP API | [Z.ai](zai/README.md) |
| requesty | HTTP API | [Requesty](requesty/README.md) |
| vikasit | HTTP API | [Vikasit](vikasit/README.md) |
| openrouter | HTTP API | [OpenRouter](openrouter/README.md) |
| groq | HTTP API | [Groq](groq/README.md) |
| gemini_api | HTTP API | [Gemini API](gemini_api/README.md) |
| novita | HTTP API | [Novita](novita/README.md) |
| opencode | 공식 CLI HTTP·SSE | [OpenCode](opencode/README.md) |
| cline | 공식 CLI ACP | [Cline](cline/README.md) |

auth.json은 공개 인증 안내이며 키·토큰 파일이 아닙니다.

## 코드 위치

| 파일 | 책임 |
| --- | --- |
| [client.go](client.go) · [types.go](types.go) | 백엔드용 Go API·호출 검증·수명주기 |
| [adapter.go](adapter.go) · [catalog.go](catalog.go) | 어댑터 계약·업체 정의·무료 판정 |
| [native_http.go](native_http.go) | HTTP 9곳의 직접 모델 조회·추론 |
| [native_opencode.go](native_opencode.go) | 공식 서버·세션·SSE·권한 거절 |
| [native_cline.go](native_cline.go) | 공식 ACP·모델 선택·권한 거절 |
| [runtime.go](runtime.go) | 관리 설치·공식 실행 파일 발견 |
| [credentials_file.go](credentials_file.go) | 독립 도구용 선택형 암호화 인증 저장 |
| [install.go](install.go) · [versions.json](versions.json) | 네이티브 CLI 설치·업데이트·검증 버전 |
| [providerctl](../../cmd/providerctl/main.go) | 서버를 띄우지 않는 인증·모델·호출 도구 |

## 검증

```sh
go -C backend test -race ./...
```

실제 업체 검증은 무료 사용량을 소비하므로 기본 테스트에서 제외합니다.

```sh
FREEWAY_LIVE=1 go -C backend test -v -count=1 -run "^TestNativeLive$" ./internal/providers
```

Kilo는 익명 무료 경로, OpenCode·Cline은 설치된 공식 CLI와 필요한 로그인을 사용합니다. API 키가 없는 나머지 업체의 실호출·새 계정 인증까지 성공했다고 간주하지 않습니다.
