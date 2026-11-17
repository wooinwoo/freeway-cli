# Cline CLI

공급처 ID: `cline`. 인증·연결·모델 시험용 인계 자료입니다. Go 어댑터 구현 완료를 뜻하지 않습니다. 공통 실행 조건은 [상위 안내](../README.md)를 따릅니다.

## 인증과 연결

공식 CLI 설치 후 auth cline --login으로 cline auth cline을 실행합니다. 브라우저·기기 인증은 사용자가 공식 화면에서 완료합니다. 공식 CLI가 토큰 갱신을 담당합니다.

공식 CLI 설치 명령: `npm install -g cline`.

저장소 루트에서 실행합니다. 키는 화면에 표시하지 않는 터미널 입력으로 받으며 코드나 명령행 인수로 전달하지 않습니다.

```sh
node backend/internal/providers/manage.mjs auth cline --login
node backend/internal/providers/manage.mjs status cline
node backend/internal/providers/manage.mjs models cline
```

## 모델 변경과 실제 요청

Go 백엔드는 공통 [공급처 패키지](../INTEGRATION.md#go-공급처-패키지-사용)에 `Provider: "cline"`을 지정합니다. `Authenticate`는 기존 공식 로그인을 재사용하고, `Login`은 운영 터미널에서 공식 인증을 시작합니다. 현재 ACP 브리지의 `Reasoning.Efforts`는 비어 있습니다. 추론 깊이를 지정하면 무시하지 않고 `unsupported_reasoning`으로 거절합니다. 일반 CLI의 `--thinking`을 ACP 요청에 그대로 붙이지 않습니다. 관련 [공식 프로젝트 이슈](https://github.com/cline/cline/issues/14820)를 참고합니다. `Quota`·`quota cline`은 로컬 집계이며 업체의 정확한 잔여량은 미제공이면 `null`입니다.

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models cline`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
node backend/internal/providers/manage.mjs select cline cline-free/nemotron-3.5-super-vl
node backend/internal/providers/manage.mjs probe cline --model cline-free/nemotron-3.5-super-vl
node backend/internal/providers/manage.mjs ask cline --model cline-free/nemotron-3.5-super-vl -- "한 문장으로 인사해 주세요."
```

`select`는 POC의 기본 모델을 저장합니다. `--model`은 한 요청에서만 모델을 바꿉니다. 업체를 지정한 시험은 다른 업체로 자동 전환하지 않습니다. `probe`는 실제 assistant 응답이 확인 문자열과 일치해야 통과합니다.

## 백엔드 연결 근거

- 전송: 공식 CLI의 ACP JSON-RPC 표준입출력. `--acp --provider cline --auto-approve false --compaction off`로 실행합니다.
- 모델 조회: `https://api.cline.bot/api/v1/ai/cline/recommended-models`의 `free` 배열만 사용합니다. 이 주소와 응답은 현재 POC가 사용하는 경로이며 최종 Go 계약은 아닙니다.
- 추론 순서: `initialize`, `session/new`, `session/set_model`, 선택 성공 응답 확인, `session/prompt`. ACP에서는 일반 CLI의 모델 플래그만으로 모델을 선택할 수 없습니다.
- 텍스트 전용: 요청마다 임시 작업 폴더를 사용하고 도구 권한 요청을 거부합니다. 자식 환경의 `CLINE_PROVIDER=cline`을 고정하고 `CLINE_API_KEY`를 제거합니다.
- 인증 저장: 공식 CLI의 사용자 홈 `.cline/data/settings/providers.json`을 사용합니다. Freeway는 토큰을 출력하거나 복사하지 않으며 갱신은 공식 CLI에 위임합니다.
- 취소: 실행한 CLI 프로세스 그룹을 종료하고 임시 작업 폴더를 제거합니다. CLI 자체의 이력 보존은 별개입니다.
- 기존 구현: [Cline 브리지](../../../../POC/src/cline.mjs), [ACP·권한·취소 검사](../../../../POC/tests/cline.test.mjs).

## 무료 판정과 오류

공식 카탈로그의 free 배열만 사용합니다. recommended와 ClinePass 목록을 무료로 취급하지 않습니다. 무료 모델은 기간·할당량에 따라 사라질 수 있습니다.

무료·한도 설명: [공식 안내](https://docs.cline.bot/getting-started/free-models). 공식 인증·API 안내: [문서](https://raw.githubusercontent.com/cline/cline/main/apps/cli/README.md). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

인증 실패, 권한 거부, 무료 한도 소진, 프로모션 종료를 성공으로 처리하지 않습니다. HTTP 429와 CLI의 무료 할당 소진은 대기·다른 경로 선택 사유이며 유료 전환 근거가 아닙니다. CLI 출력 제한은 공식 클라이언트가 결정하므로 Freeway의 `max_tokens`만으로 강제된다고 가정하지 않습니다.

## 전체 모델 실제 호출 결과

2026-11-17, 공식 `free` 배열에는 다음 3개가 있었습니다. 기존 공식 CLI 로그인과 실제 ACP 브리지를 사용했으며 모델마다 `session/set_model` 성공 후 요청했습니다. 세 모델 모두 assistant 응답이 `FREEWAY_PROVIDER_OK`와 일치하고 `end_turn`으로 완료됐습니다. 브리지가 비용·토큰 사용량을 반환하지 않으므로 비용 수치를 별도로 확인했다고 주장하지 않습니다.

| upstreamId | 결과 |
| --- | --- |
| `cline-free/nemotron-3.5-super-vl` | 성공 |
| `cline-free/solar-mini4` | 성공 |
| `nvidia/nemotron-3.5-lightning-free` | 성공 |
