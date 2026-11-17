# Novita

공급처 ID: `novita`. 인증·연결·모델 시험용 인계 자료입니다. Go 어댑터 구현 완료를 뜻하지 않습니다. 공통 실행 조건은 [상위 안내](../README.md)를 따릅니다.

## 인증과 연결

Novita 계정의 Key Management에서 키를 발급해 NOVITA_API_KEY로 전달합니다. 해당 모델의 무료 사용 조건 확인 뒤 --confirm-free를 지정합니다.

저장소 루트에서 실행합니다. 키는 화면에 표시하지 않는 터미널 입력으로 받으며 코드나 명령행 인수로 전달하지 않습니다.

```sh
node backend/internal/providers/manage.mjs auth novita --confirm-free
node backend/internal/providers/manage.mjs status novita
node backend/internal/providers/manage.mjs models novita
```

이미 `NOVITA_API_KEY` 환경변수가 준비되어 있으면 `node backend/internal/providers/manage.mjs auth novita --from-env --confirm-free`로 가져옵니다. 비대화형 입력은 `auth novita --stdin --confirm-free`을 사용합니다.

## 모델 변경과 실제 요청

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models novita`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
node backend/internal/providers/manage.mjs select novita apodex/apodex-1.1-mini
node backend/internal/providers/manage.mjs probe novita --model apodex/apodex-1.1-mini
node backend/internal/providers/manage.mjs ask novita --model apodex/apodex-1.1-mini -- "한 문장으로 인사해 주세요."
```

`select`는 POC의 기본 모델을 저장합니다. `--model`은 한 요청에서만 모델을 바꿉니다. 업체를 지정한 시험은 다른 업체로 자동 전환하지 않습니다. `probe`는 실제 assistant 응답이 확인 문자열과 일치해야 통과합니다.

## 백엔드 연결 근거

- 요청 기본 주소: `https://api.novita.ai/openai` (현재 POC 설정).
- 인증: `Authorization: Bearer`와 계정 API 키. API 키가 필요합니다.
- 모델 조회: 실제 카탈로그를 조회하지 않고 허용 목록 `apodex/apodex-1.1-mini`를 등록합니다. 이 작업은 키 유효성을 검증하지 않습니다.
- 추론: `POST https://api.novita.ai/openai/chat/completions`. 공급처의 `upstreamId`를 `model`로 보냅니다. JSON 응답 또는 SSE 응답을 기존 어댑터가 처리합니다.
- 취소: HTTP 요청의 AbortSignal을 중단합니다. Go 이식 시 HTTP 요청 context 취소를 연결해야 합니다.
- 기존 구현: [공통 API 어댑터](../../../../POC/src/adapters.mjs), [공급처 정의·무료 필터](../../../../POC/src/catalog.mjs).

## 무료 판정과 오류

POC 허용 목록은 apodex/apodex-1.1-mini입니다. 무료 가격표와 계정 조건을 확인합니다. 가입 크레딧과 해당 모델의 무료 가격은 별개입니다.

무료·한도 설명: [공식 안내](https://novita.ai/pricing). 공식 인증·API 안내: [문서](https://docs.novita.ai/api-reference/basic-authentication). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

인증 실패, 권한 거부, 무료 한도 소진, 프로모션 종료를 성공으로 처리하지 않습니다. HTTP 429와 CLI의 무료 할당 소진은 대기·다른 경로 선택 사유이며 유료 전환 근거가 아닙니다. CLI 출력 제한은 공식 클라이언트가 결정하므로 Freeway의 `max_tokens`만으로 강제된다고 가정하지 않습니다.
