# Groq

공급처 ID: `groq`. 공통 실행 순서와 Go 사용법은 [공급처 README](../README.md)를 따릅니다.

## 인증과 연결

Groq Console에서 키를 발급해 GROQ_API_KEY로 전달합니다. 계정이 무료 플랜임을 확인한 뒤 auth 명령에 --confirm-free를 지정합니다.

```sh
freeway-providers auth groq --confirm-free
freeway-providers status groq
freeway-providers models groq
```

이미 `GROQ_API_KEY` 환경변수가 준비되어 있으면 `freeway-providers auth groq --from-env --confirm-free`로 가져옵니다. 비대화형 입력은 `auth groq --stdin --confirm-free`을 사용합니다.

## 모델 변경과 실제 요청

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models groq`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
freeway-providers select groq openai/gpt-oss-20b
freeway-providers probe groq --model openai/gpt-oss-20b
freeway-providers ask groq --model openai/gpt-oss-20b -- "한 문장으로 인사해 주세요."
```

## 백엔드 연결 근거

- 요청 기본 주소: `https://api.groq.com/openai/v1` (현재 Go 어댑터 설정).
- 인증: `Authorization: Bearer`와 계정 API 키. API 키가 필요합니다.
- 모델 조회: 실제 카탈로그를 조회하지 않고 허용 목록 `openai/gpt-oss-120b`, `openai/gpt-oss-20b`를 등록합니다. 이 작업은 키 유효성을 검증하지 않습니다.
- 추론: `POST https://api.groq.com/openai/v1/chat/completions`. 공급처의 `upstreamId`를 `model`로 보냅니다. JSON 응답 또는 SSE 응답을 Go 어댑터가 처리합니다.
- 취소: Go HTTP 요청의 context를 취소해 연결·스트림을 중단합니다.
- Go 구현: [Go HTTP 어댑터](../native_http.go), [Go 공급처 정의·무료 필터](../catalog.go).

## 무료 판정과 오류

무료 계정의 모델별 한도를 사용합니다. 현재 어댑터 허용 목록은 openai/gpt-oss-120b와 openai/gpt-oss-20b입니다. 같은 모델이어도 유료 플랜이면 무료 호출로 단정할 수 없습니다.

무료·한도 설명: [공식 안내](https://console.groq.com/docs/rate-limits). 공식 인증·API 안내: [문서](https://console.groq.com/docs/quickstart). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

인증 실패, 권한 거부, 무료 한도 소진, 프로모션 종료를 성공으로 처리하지 않습니다. HTTP 429와 CLI의 무료 할당 소진은 대기·다른 경로 선택 사유이며 유료 전환 근거가 아닙니다. CLI 출력 제한은 공식 클라이언트가 결정하므로 Freeway의 `max_tokens`만으로 강제된다고 가정하지 않습니다.
