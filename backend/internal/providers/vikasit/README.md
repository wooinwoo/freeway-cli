# Vikasit

공급처 ID: `vikasit`. 공통 실행 순서와 Go 사용법은 [공급처 README](../README.md)를 따릅니다.

## 인증과 연결

Inference API 안내의 Get an API key에서 로그인하고 키를 발급해 VIKASIT_API_KEY로 전달합니다. 무료 Nova 계정 조건 확인 뒤 --confirm-free를 지정해야 probe/ask가 실행됩니다.

```sh
freeway-providers auth vikasit --confirm-free
freeway-providers status vikasit
freeway-providers models vikasit
```

이미 `VIKASIT_API_KEY` 환경변수가 준비되어 있으면 `freeway-providers auth vikasit --from-env --confirm-free`로 가져옵니다. 비대화형 입력은 `auth vikasit --stdin --confirm-free`을 사용합니다.

## 모델 변경과 실제 요청

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models vikasit`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
freeway-providers select vikasit vikasit-nova
freeway-providers probe vikasit --model vikasit-nova
freeway-providers ask vikasit --model vikasit-nova -- "한 문장으로 인사해 주세요."
```

## 백엔드 연결 근거

- 요청 기본 주소: `https://api.vikasit.ai/v1` (현재 Go 어댑터 설정).
- 인증: `Authorization: Bearer`와 계정 API 키. API 키가 필요합니다.
- 모델 조회: 실제 카탈로그를 조회하지 않고 허용 목록 `vikasit-nova`를 등록합니다. 이 작업은 키 유효성을 검증하지 않습니다.
- 추론: `POST https://api.vikasit.ai/v1/chat/completions`. 공급처의 `upstreamId`를 `model`로 보냅니다. JSON 응답 또는 SSE 응답을 Go 어댑터가 처리합니다.
- 취소: Go HTTP 요청의 context를 취소해 연결·스트림을 중단합니다.
- Go 구현: [Go HTTP 어댑터](../native_http.go), [Go 공급처 정의·무료 필터](../catalog.go).

## 무료 판정과 오류

Go 어댑터는 vikasit-nova만 허용합니다. 공식 무료 일일 토큰 한도와 계정의 유료 전환 조건을 확인해야 합니다. 다른 Vikasit·재판매 모델은 유료일 수 있습니다.

무료·한도 설명: [공식 안내](https://vikasit.ai/). 공식 인증·API 안내: [문서](https://vikasit.ai/inference). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

인증 실패, 권한 거부, 무료 한도 소진, 프로모션 종료를 성공으로 처리하지 않습니다. HTTP 429와 CLI의 무료 할당 소진은 대기·다른 경로 선택 사유이며 유료 전환 근거가 아닙니다. CLI 출력 제한은 공식 클라이언트가 결정하므로 Freeway의 `max_tokens`만으로 강제된다고 가정하지 않습니다.
