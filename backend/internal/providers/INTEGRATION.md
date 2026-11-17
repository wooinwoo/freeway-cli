# 백엔드에서 공급처를 사용하는 방법

기준일: 2026-11-17. 현재 실행 가능한 공급처 구현은 Node.js POC에 있습니다. 이 폴더의 `manage.mjs`는 인증·모델 조회·선택·점검 도구이며 Go 패키지가 아닙니다. `backend/cmd/freewayd/main.go`는 안내 후 종료하는 개발 틀입니다. 아래는 현재 POC를 이용하는 방법이며 최종 Go 내부 인터페이스나 `api/openapi.yaml`의 확정 계약이 아닙니다.

## 연결 순서

```text
초기 설정·운영 점검
  백엔드 운영 도구 → manage.mjs → 기존 로컬 POC 게이트웨이
                                    ├─ 공급처 키 암호화 저장
                                    ├─ 공식 CLI 연결
                                    └─ 무료 모델 동기화·실제 응답 점검

실제 사용자 요청
  백엔드 → 로컬 게이트웨이 /v1/chat/completions
              └─ 무료 필터·라우터·한도 예약
                  ├─ Kilo 등 HTTP API 어댑터
                  ├─ OpenCode 공식 서버 브리지
                  └─ Cline 공식 ACP 브리지
```

인증과 모델 동기화는 초기 설정·사용자 변경 시 실행합니다. 요청마다 `auth all`이나 `select`를 실행하지 않습니다. 실제 요청에는 선택한 모델 ID와 대화 내역을 명시합니다. 현재 게이트웨이는 사용자의 PC에서 `127.0.0.1`에 실행하는 구성입니다.

## 1. 공급처 준비

Node.js 22 이상이 필요합니다. OpenCode·Cline을 사용할 PC에는 공식 CLI와 필요한 로그인이 있어야 합니다. 아래 절대 경로는 이 개발 환경의 예시이므로 설치 위치에 맞게 바꿉니다.

```sh
/home/rst010/.local/bin/node /home/rst010/freeway-cli/backend/internal/providers/manage.mjs auth kilo
/home/rst010/.local/bin/node /home/rst010/freeway-cli/backend/internal/providers/manage.mjs auth opencode
/home/rst010/.local/bin/node /home/rst010/freeway-cli/backend/internal/providers/manage.mjs auth cline --login
/home/rst010/.local/bin/node /home/rst010/freeway-cli/backend/internal/providers/manage.mjs models kilo
/home/rst010/.local/bin/node /home/rst010/freeway-cli/backend/internal/providers/manage.mjs probe kilo --model kilo-auto/free --max-tokens 2048
```

`auth cline --login`은 대화형 터미널에서 실행합니다. 이미 로그인한 경우에는 `auth cline`으로 연결만 합니다. 웹 요청 처리 중 브라우저 로그인을 실행하지 않습니다. API 키 입력·무료 계정 확인·전체 업체 명령은 [운영 안내](README.md), 공식 CLI 설치와 프로토콜은 각 [업체 문서](README.md#업체별-안내)에 있습니다.

관리 명령은 기존 게이트웨이를 재사용하고 필요하면 자동 시작합니다. `FREEWAY_URL`은 `http://127.0.0.1:4141`과 같은 서버 원점이며 `/v1`을 붙이지 않습니다. `FREEWAY_DATA_DIR`은 게이트웨이를 시작하는 프로세스에서 지정합니다. 이미 실행 중인 서버의 데이터 디렉터리는 클라이언트 환경변수만 바꿔서 변경되지 않습니다. 같은 데이터 디렉터리에는 게이트웨이 하나만 실행합니다.

### Go에서 관리 명령 실행

Go에서는 `exec.CommandContext(ctx, nodeAbsolutePath, manageAbsolutePath, "models", "kilo")`처럼 실행 파일과 argv를 분리합니다. 셸 문자열로 이어 붙이지 않습니다. 로그인 외 작업은 제한 시간을 설정하고 stdout의 JSON 전체를 디코딩합니다. stderr는 운영 안내용이며 JSON으로 파싱하지 않습니다. 숨김 입력·로그인 경로의 stderr에 비밀 값을 추가해 출력하지 않습니다.

| 작업 | argv | 백엔드가 확인할 결과 |
| --- | --- | --- |
| API 키 구성 | `auth`, 업체 ID, `--stdin` | 비밀 저장 계층에서 읽은 키를 자식 stdin으로 전달. stdout의 `ok`, `results[].ready`, `reason` 확인 |
| 환경변수 키 일괄 구성 | `auth`, `all`, `--from-env` | 자식 환경에 준비한 키만 전달. `ok: false`여도 업체별 `results` 확인 |
| CLI 연결 | `auth`, `opencode` 또는 `cline` | 기존 공식 로그인 사용. 로그인이 없으면 대화형 설정으로 안내 |
| 연결 상태 | `status`, 업체 ID | `enabled`, `ready`, `reason`, `hasKey`, `freeConfirmed` 확인 |
| 무료 모델 조회 | `models`, 업체 ID | `results[].models[]`에서 `id`, `upstreamId`, `ready`, `reason`, `commands` 사용 |
| 실제 추론 점검 | `probe`, 업체 ID, `--model`, upstreamId | `status: passed`와 `inferenceVerified: true` 확인 |

정상 처리 여부는 종료 코드와 JSON의 `ok`를 함께 확인합니다. 실패한 명령도 유효한 JSON을 stdout에 반환할 수 있으므로 Go의 `cmd.Output()`이 `ExitError`를 반환해도 받은 stdout을 버리지 않습니다. 실행 파일 없음·강제 종료·제한 시간 초과처럼 JSON이 없는 실패도 별도로 처리합니다. 관리 명령 한 번은 JSON 객체 하나를 출력하며 JSON Lines나 SSE가 아닙니다.

API 키는 argv에 넣지 않습니다. 키가 바뀌면 이전 무료 계정 확인을 초기화하도록 `manage.mjs auth` 경로를 사용합니다. 관리 HTTP의 `PUT /api/providers/ID`를 직접 사용할 때는 같은 요청에 `freeConfirmed: false`를 명시하고 새 계정의 조건을 다시 확인해야 합니다. 원시 관리 API는 이 초기화를 자동 보장하지 않습니다.

## 2. 모델 ID 선택

| 값 | 의미 | 사용 위치 |
| --- | --- | --- |
| `kilo/kilo-auto/free` | 업체까지 고정한 무료 경로 ID | 특정 업체·모델 점검과 고정 호출 |
| `kilo-auto/free` | 공급처의 upstreamId | `manage.mjs`에 업체 ID와 함께 전달. HTTP의 `model`에는 전체 경로 ID 사용 |
| `model/...` | 같은 기반 모델의 무료 연결들을 묶은 그룹 ID | `/v1/models`가 실제 반환한 ID 사용. 그룹 안에서 다른 업체 경로로 전환 가능 |
| `free/auto` | 무료 모델 자동 선택 | 게이트웨이의 우선순위·가용성·한도에 따라 선택 |

`GET /v1/models`는 `free/auto`와 무료 모델 그룹을 반환합니다. 업체별 경로는 각 그룹의 `routes[]`에 있습니다. 특정 업체를 고정하려면 반환된 `routes[].id` 또는 `manage.mjs models`의 `models[].id`를 사용합니다. 표시 이름으로 ID를 만들거나 그룹 ID를 추측하지 않습니다.

모델 목록에서 `free: true`와 `ready: true`는 호출 가능 조건을 나타냅니다. 실제 응답 성공이나 앞으로의 가용성을 보장하지 않습니다. `/v1/models`의 그룹·경로에는 `ready`가 있으며, 공급처 무료 목록을 반환하는 `manage.mjs models`는 무료 필터를 통과한 모델만 포함합니다. OpenCode `iceman-free`처럼 목록에 있어도 실제 호출이 실패할 수 있습니다.

`select 업체 모델`은 POC의 **공유 기본 설정**을 저장합니다. `ask`·`probe`가 모델을 생략했을 때 해당 업체의 저장 모델을 우선 사용합니다. 백엔드의 HTTP 요청에는 `model`을 매번 넣습니다. 대화별 모델을 바꾸려고 공유 `select`를 덮어쓰지 않습니다. 현재 원시 `/v1/chat/completions`에서 `model`을 생략하면 `free/auto`를 사용하며 저장한 `chatDefaults.model`을 자동 적용하지 않습니다.

## 3. 요청 처리: 로컬 HTTP API

호출 주소는 `http://127.0.0.1:4141/v1`입니다. 여기의 Bearer 키는 **Freeway 로컬 API 키**이며 `KILO_API_KEY` 등 공급처 키와 다릅니다. 기존 관리 화면의 연결 정보에서 확인하거나, 인증된 로컬 관리 클라이언트의 `GET /api/key` 결과를 비밀 저장 계층에 전달합니다. 키·쿠키·인증 헤더는 로그와 응답에 노출하지 않습니다.

아래 Go 예제는 공급처 연결이 끝난 뒤 실행합니다. `FREEWAY_API_KEY`는 이 예제를 실행하는 프로세스에 비밀 저장 계층이 주입하는 값입니다. 별도 HTTP 서비스의 사용자 요청 본문으로 공급처 키를 받거나, 소스 코드에 키를 넣는 예제가 아닙니다.

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "time"
)

func main() {
    if err := request(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func request() error {
    key := os.Getenv("FREEWAY_API_KEY")
    if key == "" {
        return fmt.Errorf("FREEWAY_API_KEY is required")
    }
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    payload, err := json.Marshal(map[string]any{
        "model": "kilo/kilo-auto/free",
        "messages": []map[string]string{
            {"role": "user", "content": "Reply with exactly FREEWAY_PROVIDER_OK. Do not use tools."},
        },
        "max_tokens": 2048,
        "stream": false,
    })
    if err != nil {
        return err
    }
    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        "http://127.0.0.1:4141/v1/chat/completions", bytes.NewReader(payload))
    if err != nil {
        return err
    }
    req.Header.Set("Authorization", "Bearer "+key)
    req.Header.Set("Content-Type", "application/json")
    client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
        return http.ErrUseLastResponse
    }}
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    var result struct {
        Model    string `json:"model"`
        Provider string `json:"provider"`
        Choices  []struct {
            Message struct { Content string `json:"content"` } `json:"message"`
        } `json:"choices"`
        Error *struct { Code string `json:"code"` } `json:"error"`
    }
    if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
        return err
    }
    if resp.StatusCode != http.StatusOK || result.Error != nil {
        code := "unknown"
        if result.Error != nil {
            code = result.Error.Code
        }
        return fmt.Errorf("Freeway HTTP %d (%s)", resp.StatusCode, code)
    }
    if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
        return fmt.Errorf("Freeway returned no answer")
    }
    fmt.Println(result.Choices[0].Message.Content)
    return nil
}
```

실서비스에서는 고정한 `messages`를 해당 대화의 이력으로, `model`을 선택한 ID로 바꾸고 결과를 호출자에게 반환합니다. `context`는 사용자 요청의 취소와 연결합니다. `max_tokens`는 HTTP API 공급처의 요청 상한이며 OpenCode·Cline CLI에는 같은 상한이 강제되지 않습니다. 출력 `model`과 `provider`는 실제 사용한 경로를 기록하는 데 사용합니다. `usage`는 공급처에 따라 생략될 수 있으며, 없는 값을 사용량·비용 0으로 저장하지 않습니다.

### Node 코드에서 재사용

현재 POC 안에서 코드를 연결한다면 기존 `Client`를 재사용할 수 있습니다. 이 경로는 로컬 관리 쿠키를 내부적으로 사용하므로 별도 Freeway API 키를 읽을 필요가 없습니다. `Client.complete()`는 내부적으로 SSE를 소비하고 완료된 결과를 반환합니다.

```js
import { Client } from '/home/rst010/freeway-cli/POC/src/client.mjs';

const client = new Client({ base: 'http://127.0.0.1:4141', autoStart: false });
const result = await client.complete({
  model: 'kilo/kilo-auto/free',
  messages: [{ role: 'user', content: 'Reply with exactly FREEWAY_PROVIDER_OK. Do not use tools.' }],
  max_tokens: 2048
}, { signal: AbortSignal.timeout(60000) });
console.log(result.content);
```

Go TUI가 사용하는 기존 예제는 [poc.go](../../../cli/client/poc.go)와 [bridge.mjs](../../../cli/client/bridge.mjs)입니다. TUI용 브리지는 표준입력 JSON 하나를 받아 stdout에 이벤트를 한 줄씩 출력합니다. `manage.mjs`의 단일 JSON 결과와 혼동하지 않습니다. Node 모듈을 Go에서 import할 수 있다는 뜻도 아닙니다.

## 4. 스트림·실패·취소

| 상황 | 처리 |
| --- | --- |
| 스트리밍 요청 | `stream: true`. SSE의 `data:` JSON에서 `choices[].delta.content`를 전달하고 `data: [DONE]`으로 완료 확인 |
| 스트림 시작 전 실패 | HTTP 오류 상태와 `{error:{message,type,code}}` JSON 확인 |
| 스트림 시작 후 실패 | HTTP 200이어도 SSE의 `{error:...}`를 실패로 처리. 뒤에 `[DONE]`이 와도 성공으로 바꾸지 않음 |
| HTTP 401 | Freeway 키 오류인지 공급처 로그인 오류인지 `error.code`로 구분. 키를 출력하지 않고 해당 인증 경로로 안내 |
| HTTP 429 | 공급처·모델의 무료 한도 응답. `Retry-After`가 있으면 준수. 한도 변경·유료 모델 전환을 자동 수행하지 않음 |
| HTTP 503 `provider_not_ready` | 미연결·무료 조건 미확인·로컬 cooldown 등을 상태 조회로 확인. HTTP 503만 보고 무료 모델이 없다고 단정하지 않음 |
| HTTP 404 `model_not_found` | 현재 무료 목록에 없는 경로. 모델 목록을 다시 조회하고 사용자 선택을 갱신 |
| HTTP 400 `unsupported_capability` | CLI 브리지의 비텍스트 입력 거절이나 OpenCode의 실제 도구 권한 요청 거절. 같은 입력을 무한 재시도하지 않음 |
| CLI 경로에 `tools`·도구 이력 전달 | HTTP 라우터가 도구 미지원 경로를 먼저 제외. 후보가 없으면 `503 no_free_models`, 다른 경로에 한도 상태가 있으면 `429 rate_limit_exceeded`. 브리지 직접 호출의 400과 구분 |
| 사용자 취소·제한 시간 | Go HTTP request context 또는 Node AbortSignal 취소. 게이트웨이가 HTTP 요청 중단·OpenCode 세션 중단·Cline 자식 프로세스 종료를 담당 |

클라이언트가 연결을 닫으면 게이트웨이가 취소를 전달합니다. 이미 전송한 공급처 요청의 사용량까지 되돌린다는 뜻은 아닙니다. 응답 일부를 호출자에게 전달한 뒤 새 요청을 자동 재시도하면 중복 출력·추가 사용량이 생길 수 있습니다. 현재 라우터는 출력 시작 후 다른 경로로 이어 붙이지 않습니다.

## 5. 관리 API와 Go 이식 경계

| 현재 관리 API | 동작·인증 |
| --- | --- |
| `GET /api/state` | 공급처·무료 모델·전체 카탈로그·설정·관측 상태. 키 원문 제외 |
| `GET /api/key` | Freeway 로컬 API 키. 응답을 출력하거나 일반 상태 응답에 합치지 않음 |
| `PUT /api/providers/ID` | API 키·활성화·무료 확인 등 설정 |
| `POST /api/providers/ID/sync` | API 공급처 모델 동기화 |
| `POST /api/providers/opencode/connect`, `POST /api/providers/cline/connect` | 공식 CLI 연결 후 모델 동기화 |
| `PUT /api/settings` | 공유 기본 모델·우선순위·제한 시간 설정 |

`/api/*`는 로컬 관리 쿠키가 필요합니다. 변경 요청에는 같은 서버 원점의 `Origin`도 필요합니다. `/v1/*`에 쓰는 Bearer 키만으로 관리 API를 호출할 수 없습니다. 쿠키 획득·401 재연결은 [기존 Client](../../../POC/src/client.mjs)가 담당합니다. HTTP·검증·SSE의 실제 구현은 [server.mjs](../../../POC/server.mjs)를 따릅니다.

Go로 옮길 때는 아래 기존 구현을 출발점으로 사용합니다. 관리 스크립트나 POC JSON을 최종 Go 인터페이스로 복사하지 않습니다.

| 책임 | 현재 참고 코드 | 최종 담당 영역 |
| --- | --- | --- |
| 공급처 목록·무료 판정·모델 정규화 | [catalog.mjs](../../../POC/src/catalog.mjs), 업체별 README·auth.json | `providers/`, 공통 타입은 `contracts/` |
| HTTP 모델 조회·완료·SSE | [adapters.mjs](../../../POC/src/adapters.mjs) | `providers/` |
| CLI 인증 확인·모델 선택·권한 거절·취소 | [opencode.mjs](../../../POC/src/opencode.mjs), [cline.mjs](../../../POC/src/cline.mjs) | `providers/` |
| 모델 그룹·fallback·오류 후 cooldown | [engine.mjs](../../../POC/src/engine.mjs), [model-groups.mjs](../../../POC/src/model-groups.mjs) | `router/`와 `quota/` |
| 비밀 저장·사용량 원장 | [store.mjs](../../../POC/src/store.mjs) | `auth/`, `quota/`, `storage/` |
| 시작·종료·단일 게이트웨이 소유 | [server.mjs](../../../POC/server.mjs), [client.mjs](../../../POC/src/client.mjs) | `app/` |

공급처 어댑터는 자체 fallback이나 별도 사용량 원장을 만들지 않습니다. 인터페이스·예약·정산·저장 책임은 [공통 구조 문서](../../../docs/development/architecture.md#의존성과-책임)를 따릅니다. `auth.json`은 공개 인증 안내이지 자격증명 파일이 아닙니다. `.data`와 공식 CLI의 로그인 파일은 버전 관리나 별도 backend 복사본에 포함하지 않습니다.

## 문서 예제 검증

2026-11-17, 위 Go 코드 블록을 그대로 추출해 `go build`한 뒤 기존 로컬 게이트웨이에서 실제 확인 응답을 받았습니다. API 키 누락 시 요청 전 실패, 잘못된 Freeway 키의 HTTP 401 처리도 확인했습니다. Node 코드 블록도 그대로 실행해 SSE 경로의 확인 응답을 받았고, `/v1/models`가 반환하는 `routes[].id`에서 예제의 고정 경로를 확인했습니다. 검증 과정에서 키는 프로세스 메모리·자식 환경으로만 전달하고 출력하지 않았습니다.

공급처 검증 범위와 실제 모델별 결과는 [운영 안내의 검증](README.md#검증)을 따릅니다. 현재 실호출 근거가 있는 공급처는 Kilo·OpenCode·Cline이며 나머지 API 8곳은 키가 없어 추론 미검증입니다. Windows의 실제 인증·호출과 Go 공급처 구현 완료를 보장하지 않습니다.
