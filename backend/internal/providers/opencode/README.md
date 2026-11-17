# OpenCode

공급처 ID: `opencode`. 공통 실행 순서와 Go 사용법은 [공급처 README](../README.md)를 따릅니다.

## 인증과 연결

공식 CLI를 먼저 설치합니다. 자체 무료 모델은 auth opencode로 로컬 서버를 연결합니다. 계정 인증이 필요한 경우 auth opencode --login은 opencode auth login을 실행합니다.

설치·버전 확인·업데이트는 [공통 설치기](../../../../scripts/install/README.md)의 `--only opencode`를 사용합니다. 수동 공식 npm 설치가 필요한 경우 검증 버전은 `npm install -g opencode-ai@1.18.35`입니다.

현재 POC 브리지는 V1 서버 API를 사용하며 WSL의 1.18.35에서 검증했습니다. 최신 V2 패키지 `@opencode/cli`의 서버 API를 지원한다는 뜻은 아닙니다. 정상 사용 중인 설치를 이 검증 때문에 교체할 필요는 없습니다.

```sh
node backend/internal/providers/manage.mjs auth opencode
node backend/internal/providers/manage.mjs status opencode
node backend/internal/providers/manage.mjs models opencode
```

## 모델 변경과 실제 요청

Go 백엔드는 공통 [공급처 패키지](../README.md#go에서-사용)에 `Provider: "opencode"`를 지정합니다. `Request.ReasoningEffort`는 모델 조회의 `Reasoning.Efforts`에 있는 공식 `variant` 값으로 전달합니다. DeepSeek의 `low/max`를 실제 Go 코드로 확인했습니다. 지원값이 없는 모델에는 추론 깊이를 지정하지 않습니다. `Quota`·`quota opencode`는 로컬 관측이며 업체의 정확한 무료 잔여량을 보장하지 않습니다.

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models opencode`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
node backend/internal/providers/manage.mjs select opencode deepseek-v4.1-flash-free
node backend/internal/providers/manage.mjs probe opencode --model deepseek-v4.1-flash-free
node backend/internal/providers/manage.mjs ask opencode --model deepseek-v4.1-flash-free -- "한 문장으로 인사해 주세요."
```

## 백엔드 연결 근거

- 전송: 공식 CLI의 인증된 loopback HTTP 서버. `serve --hostname 127.0.0.1 --port PORT`를 실행하고 요청마다 `Basic` 인증을 사용합니다.
- 모델 조회: `/provider`, 실패 시 `/config/providers`. `id=opencode`의 비활성화·폐기되지 않은 단가 0 텍스트 모델만 선택합니다.
- 추론: `/session` 생성, `/event` 구독, `/session/ID/message`에 `{providerID, modelID}`를 명시합니다. 텍스트 전용이며 자동 제목·요약·압축을 비활성화합니다. 세션·에이전트의 권한은 기본 거부, 읽기만 승인 대기입니다. 읽기 권한 요청이 오면 `/permission/ID/reply`에 `reply: reject`를 보내고 세션을 중지합니다.
- `tools` 옵션은 세션 권한을 `allow`로 바꿀 수 있으므로 메시지 요청에 보내지 않습니다. 사고 과정은 텍스트 delta로 넘기지 않으며, 최종 응답 뒤 늦게 온 delta도 중복 표시하지 않습니다.
- 취소: `/session/ID/abort` 후 세션을 삭제합니다. 관리 중인 서버의 시작·종료는 기존 브리지가 담당합니다.
- 기존 구현: [OpenCode 브리지](../../../../POC/src/opencode.mjs), [회귀 검사](../../../../POC/tests/opencode.test.mjs).

## 무료 판정과 오류

OpenCode 자체 공급처의 입출력 단가 0인 모델만 사용합니다. 무료 프로모션 종료·한도 소진을 구분합니다. 다른 공급처 키는 이 경로에 혼합하지 않습니다.

무료·한도 설명: [공식 안내](https://opencode.ai/v1/docs/server/). 공식 인증·API 안내: [문서](https://opencode.ai/docs/cli/). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

## 연결 설정 비교 결과

2026-11-17, WSL의 같은 OpenCode 1.18.35와 `opencode/deepseek-v4.1-flash-free`로 비교했습니다. 기본 TUI는 확인 응답을 받았고, 다음 기본 CLI 호출도 `FREEWAY_PROVIDER_OK`를 반환했습니다. CLI 결과의 완료 사유는 `stop`, 보고 비용은 0이었습니다.

```sh
opencode run --model opencode/deepseek-v4.1-flash-free --format json --title "Freeway diagnostic" "Reply with exactly FREEWAY_PROVIDER_OK. Do not use tools."
```

다른 조건을 유지하고 설정을 나눠 비교한 결과는 다음과 같습니다.

| 설정 | 실제 결과 |
| --- | --- |
| 기본 설정 | 정상 응답 |
| 제목·요약·압축 보조 에이전트만 비활성화 | 정상 응답 |
| `permission: {"*":"deny"}`만 추가 | 로그에 무료 제한 오류, 응답 없이 재시도 대기 |
| 전체 권한 거부를 제거하고 다시 요청 | 정상 응답, 보고 비용 0 |

기존 [브리지 설정](../../../../POC/src/opencode.mjs)은 전체 권한 거부와 별도 `freeway` 에이전트·도구 비활성화를 적용했습니다. OpenCode의 [요청 구성 코드](https://github.com/anomalyco/opencode/blob/v1.18.35/packages/opencode/src/session/llm/request.ts)는 거부된 도구를 요청의 도구 목록에서 제외합니다. 서버가 왜 이 요청을 무료 제한 오류로 거절하는지 내부 기준은 확인되지 않았습니다. 이 결과를 계정·IP의 실제 할당량 소진으로 단정하지 않습니다.

수정본은 모든 도구를 기본 거부하고 `read` 스키마만 `ask`로 남깁니다. 실제 읽기 권한은 승인하지 않으며 `permission.asked`를 받으면 거절 후 중지합니다. `steps: 1`은 첫 호출에서 도구 목록을 다시 없앨 수 있으므로 제거했습니다. 클라이언트가 외부 도구 호출을 요청하면 기존과 같이 실행 전에 거절합니다.

수정 후 격리된 실제 게이트웨이에서 다음을 확인했습니다.

- `probe opencode --model deepseek-v4.1-flash-free`: 확인 문자열 수신, 보고 비용 0.
- `select opencode mimo-v2.6-flash-free` 후 `probe opencode`: 변경한 모델의 확인 문자열 수신, 보고 비용 0.
- 읽기를 유도하는 별도 시험용 에이전트 지침에서도 읽기 권한 요청이 실제 발생했고 `reply: reject`가 HTTP 200으로 처리됐습니다. 요청은 `unsupported_capability`로 중단됐으며 임시 파일 내용은 응답에 포함되지 않았습니다. 이 시험은 임시 서버의 지침만 바꿨고 실제 권한 규칙과 브리지 코드는 그대로 사용했습니다.

별도 v2.3.0 시험도 전체 권한 거부를 포함한 설정에서 실행했으므로, 그 실패만으로 버전·계정·네트워크를 원인으로 확정할 수 없습니다.
