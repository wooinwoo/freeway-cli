# OpenCode

공급처 ID: `opencode`. 공통 실행 순서와 Go 사용법은 [공급처 README](../README.md)를 따릅니다.

## 인증과 연결

공식 CLI를 먼저 설치합니다. 자체 무료 모델은 auth opencode로 로컬 서버를 연결합니다. 계정 인증이 필요한 경우 auth opencode --login은 opencode auth login을 실행합니다.

설치·버전 확인·업데이트는 [공통 설치기](../../../../scripts/install/README.md)의 `--only opencode`를 사용합니다. 공식 네이티브 패키지를 Go 설치기가 내려받아 검증하므로 Node·npm은 필요하지 않습니다.

현재 Go 어댑터는 V1 서버 API를 사용하며 WSL의 1.18.35에서 검증했습니다. 최신 V2 패키지 `@opencode/cli`의 서버 API를 지원한다는 뜻은 아닙니다. 정상 사용 중인 설치를 이 검증 때문에 교체할 필요는 없습니다.

```sh
freeway-providers auth opencode
freeway-providers status opencode
freeway-providers models opencode
```

## 모델 변경과 실제 요청

Go 백엔드는 공통 [공급처 패키지](../README.md#go에서-사용)에 `Provider: "opencode"`를 지정합니다. `Request.ReasoningEffort`는 모델 조회의 `Reasoning.Efforts`에 있는 공식 `variant` 값으로 전달합니다. DeepSeek의 `low/max`를 실제 Go 코드로 확인했습니다. 지원값이 없는 모델에는 추론 깊이를 지정하지 않습니다. `Quota`·`quota opencode`는 로컬 관측이며 업체의 정확한 무료 잔여량을 보장하지 않습니다.

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models opencode`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
freeway-providers select opencode deepseek-v4.1-flash-free
freeway-providers probe opencode --model deepseek-v4.1-flash-free
freeway-providers ask opencode --model deepseek-v4.1-flash-free -- "한 문장으로 인사해 주세요."
```

## 백엔드 연결 근거

- 전송: 공식 CLI의 인증된 loopback HTTP 서버. `serve --hostname 127.0.0.1 --port PORT`를 실행하고 요청마다 `Basic` 인증을 사용합니다.
- 모델 조회: `/provider`, 실패 시 `/config/providers`. `id=opencode`의 비활성화·폐기되지 않은 단가 0 텍스트 모델만 선택합니다.
- 추론: `/session` 생성, `/event` 구독, `/session/ID/message`에 `{providerID, modelID}`를 명시합니다. 텍스트 전용이며 자동 제목·요약·압축을 비활성화합니다. 세션·에이전트의 권한은 기본 거부, 읽기만 승인 대기입니다. 읽기 권한 요청이 오면 `/permission/ID/reply`에 `reply: reject`를 보내고 세션을 중지합니다.
- `tools` 옵션은 세션 권한을 `allow`로 바꿀 수 있으므로 메시지 요청에 보내지 않습니다. 사고 과정은 텍스트 delta로 넘기지 않으며, 최종 응답 뒤 늦게 온 delta도 중복 표시하지 않습니다.
- 취소: `/session/ID/abort` 후 세션을 삭제합니다. Go 어댑터가 자신이 시작한 공식 CLI 서버의 수명주기를 관리합니다.
- Go 구현: [공식 서버 어댑터](../native_opencode.go), [회귀 검사](../native_opencode_test.go).

## 무료 판정과 오류

OpenCode 자체 공급처의 입출력 단가 0인 모델만 사용합니다. 무료 프로모션 종료·한도 소진을 구분합니다. 다른 공급처 키는 이 경로에 혼합하지 않습니다.

무료·한도 설명: [공식 안내](https://opencode.ai/v1/docs/server/). 공식 인증·API 안내: [문서](https://opencode.ai/docs/cli/). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

## 도구 권한과 무료 제한 오류

OpenCode는 전체 도구 스키마를 제거한 요청에 무료 제한 오류를 반환한 사례가 있었습니다. Go 어댑터는 기본 거부·read 승인 대기 스키마를 유지하되 모든 실제 권한 요청은 거절하고 세션을 중지합니다. 메시지의 tools 옵션은 권한을 allow로 바꿀 수 있어 보내지 않습니다. 이 설정을 단순화하면서 도구 실행 권한을 열지 않습니다.

## Go 직접 호출 검증

2026-11-17, Go 어댑터로 당시 무료 모델 12개 중 11개가 확인 표식을 반환했습니다. DeepSeek의 low/max도 통과했습니다. nemotron-3.5-super-vl-free는 업체 무료 제한 429를 반환했습니다. 이후 추가 DeepSeek 예제 호출에서도 429가 관측됐으며 다른 모델로 자동 전환하지 않았습니다. 이는 카탈로그 준비 상태와 실제 한도·응답 성공이 다르다는 뜻입니다.
