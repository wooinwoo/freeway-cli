# NVIDIA NIM

공급처 ID: `nvidia`. 인증·연결·모델 시험용 인계 자료입니다. Go 어댑터 구현 완료를 뜻하지 않습니다. 공통 실행 조건은 [상위 안내](../README.md)를 따릅니다.

## 인증과 연결

build.nvidia.com에서 계정으로 로그인하고 모델 화면의 Get API Key로 키를 발급합니다. NVIDIA_API_KEY로 전달합니다. 개발·평가 권한과 무료 조건을 확인한 뒤 --confirm-free를 지정해야 probe/ask가 실행됩니다.

저장소 루트에서 실행합니다. 키는 화면에 표시하지 않는 터미널 입력으로 받으며 코드나 명령행 인수로 전달하지 않습니다.

```sh
node backend/internal/providers/manage.mjs auth nvidia --confirm-free
node backend/internal/providers/manage.mjs status nvidia
node backend/internal/providers/manage.mjs models nvidia
```

이미 `NVIDIA_API_KEY` 환경변수가 준비되어 있으면 `node backend/internal/providers/manage.mjs auth nvidia --from-env --confirm-free`로 가져옵니다. 비대화형 입력은 `auth nvidia --stdin --confirm-free`을 사용합니다.

## 모델 변경과 실제 요청

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models nvidia`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
node backend/internal/providers/manage.mjs select nvidia nvidia/nemotron-3-ultra-550b-a55b
node backend/internal/providers/manage.mjs probe nvidia --model nvidia/nemotron-3-ultra-550b-a55b
node backend/internal/providers/manage.mjs ask nvidia --model nvidia/nemotron-3-ultra-550b-a55b -- "한 문장으로 인사해 주세요."
```

`select`는 POC의 기본 모델을 저장합니다. `--model`은 한 요청에서만 모델을 바꿉니다. 업체를 지정한 시험은 다른 업체로 자동 전환하지 않습니다. `probe`는 실제 assistant 응답이 확인 문자열과 일치해야 통과합니다.

## 백엔드 연결 근거

- 요청 기본 주소: `https://integrate.api.nvidia.com/v1` (현재 POC 설정).
- 인증: `Authorization: Bearer`와 계정 API 키. API 키가 필요합니다.
- 모델 조회: `GET https://integrate.api.nvidia.com/v1/models`. POC의 무료 필터를 거친 결과만 실행합니다.
- 추론: `POST https://integrate.api.nvidia.com/v1/chat/completions`. 공급처의 `upstreamId`를 `model`로 보냅니다. JSON 응답 또는 SSE 응답을 기존 어댑터가 처리합니다.
- 취소: HTTP 요청의 AbortSignal을 중단합니다. Go 이식 시 HTTP 요청 context 취소를 연결해야 합니다.
- 기존 구현: [공통 API 어댑터](../../../../POC/src/adapters.mjs), [공급처 정의·무료 필터](../../../../POC/src/catalog.mjs).

## 무료 판정과 오류

NVIDIA Developer Program의 프로토타이핑용 무료 API입니다. 상용 서비스의 무제한 무료 사용을 뜻하지 않습니다. POC는 모든 텍스트 모델을 free-prototype으로 분류하므로 가격 0 카탈로그 검증으로 취급하면 안 됩니다.

무료·한도 설명: [공식 안내](https://build.nvidia.com/). 공식 인증·API 안내: [문서](https://docs.api.nvidia.com/nim/docs/api-quickstart). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

인증 실패, 권한 거부, 무료 한도 소진, 프로모션 종료를 성공으로 처리하지 않습니다. HTTP 429와 CLI의 무료 할당 소진은 대기·다른 경로 선택 사유이며 유료 전환 근거가 아닙니다. CLI 출력 제한은 공식 클라이언트가 결정하므로 Freeway의 `max_tokens`만으로 강제된다고 가정하지 않습니다.
