# 실제 호출로 확인한 결과

실행일: 2026년 11월 16일. 이 장은 이번에 실행한 요청의 결과만 기록한다. 서비스 전체에 대한 영구 보장으로 확대하지 않는다.

## OpenRouter 무료 사용과 자동 전환

| 시험 | 입력 | 실제 결과 | 판정 |
| --- | --- | --- | --- |
| 무료 모델 자동 선택 | `model: openrouter/free` | HTTP 200, `cohere/north-mini-code:free`, 응답 본문 수신, 비용 0 USD | 무료 모델을 API로 사용할 수 있다 |
| 한도 오류 대조군 | `model: particle-ai/particle-1:free` | HTTP 429, `Provider returned error` | 이 요청은 한도 오류로 실패했다 |
| 무료 대체 목록 | `models: [particle-ai/particle-1:free, cohere/north-mini-code:free]` | HTTP 200, 실제 모델 `cohere/north-mini-code:free`, 비용 0 USD | 429 대조군과 같은 주 모델을 앞에 둔 요청이 다른 무료 모델로 성공했다 |
| DeepSeek 가격 목록 | `GET /api/v1/models` | 전체 432개, DeepSeek 20개, 입력·출력 단가가 모두 0인 DeepSeek 0개 | 해당 조회의 DeepSeek는 무료 모델 후보가 아니다 |
| 외부 모델 ID 직접 전달 | `model: opencode/deepseek-v4.1-flash-free` | HTTP 400, `is not a valid model ID` | OpenCode의 모델 ID를 그대로 OpenRouter에 전달할 수 없다 |

실제 요청에서는 응답 길이를 제한하고 대체 목록을 무료 모델만으로 구성했다. 가격 상한을 적용한 시험도 포함했다. 각 시험 묶음의 전후 키 사용액 차이는 0 USD였다. 기존에 발생했던 Ori 보조 호출 금액은 이번 직접 API 시험의 비용에 포함시키지 않는다.

429 대조군과 대체 목록 요청은 별도 요청이다. 대체 요청의 응답에는 실제 최종 모델이 표시됐으며, 그 모델은 목록의 두 번째 모델이었다. OpenRouter 내부의 개별 시도 로그를 직접 열람했다고 주장하지 않는다.

원자료: [OpenRouter 요청 결과](evidence/2026-11-16-live-probes.json). 공식 계약: [모델 대체](https://openrouter.ai/docs/guides/routing/model-fallbacks), [무료 모델](https://openrouter.ai/collections/free-models).

## OpenCode 공식 서버와 무료 DeepSeek

Freeway의 기존 `OpenCodeBridge`를 통해 공식 OpenCode 서버를 실행했다. 서버 버전은 `1.18.35`였고 모델 조회에서 무료 모델 12개를 받았다. 그중 `deepseek-v4.1-flash-free`를 선택해 텍스트 요청을 보냈다. 도구는 모두 차단하고 제목·요약·자동 압축을 비활성화한 브리지 설정을 사용했다.

공급처가 반환한 재시도 메시지는 다음과 같았다.

```text
Free usage exceeded, subscribe to Go
```

기존 브리지는 이 재시도 이벤트를 HTTP 503 계열의 `upstream_retry`로 정규화했다. 공급처 원문은 무료 할당 소진을 말하므로, Freeway 개선에서는 이를 무료 한도 소진으로 분류하는 태스크를 둔다. 이번 실행은 서버·모델 조회·실제 요청과 한도 거절까지 도달했으며, 성공 응답으로 계산하지 않는다.

원자료: [OpenCode 공식 서버 결과](evidence/2026-11-16-opencode-live.json). 코드 근거: `src/opencode.mjs`의 `modelError`, `session.status` 처리.

## Cline 재로그인과 DeepSeek 모델 거절

사용자가 직접 Cline 디바이스 로그인을 승인했고 CLI에서 로그인 성공을 확인했다. 이후 공식 CLI로 `cline-free/deepseek-v4.1-flash`를 명시해 텍스트 요청을 보냈다. plan 모드, 도구 자동 승인 해제, 자동 압축 해제, 30초 제한을 적용했다.

공급처 오류는 `model not found`였고 CLI는 다음 진단으로 종료했다.

```text
Free model promotion ended
The free promotion for this model has ended and it is no longer available.
```

종료 코드는 1, 입력·출력 토큰은 0, CLI가 보고한 총비용은 0이었다. 공급처 원문은 `model not found`이며 프로모션 종료는 공식 CLI가 붙인 진단이다. 이 요청의 결과에 따라 해당 모델을 현재 사용 가능한 무료 경로에서 제외한다. 이 결과를 Cline의 다른 모든 모델이 무료로 동작하지 않는다는 주장으로 확대하지 않는다.

원자료: [Cline 실행 결과](evidence/2026-11-16-cline-live.json). 공식 무료 조건은 [Cline 안내](https://docs.cline.bot/getting-started/free-models)를 따른다.

## 기획에 반영하는 결론

1. 무료 자동 선택과 모델 대체는 OpenRouter가 이미 해결한다.
2. 해당 조회에서 OpenRouter는 무료 DeepSeek를 제공하지 않았다.
3. 외부 무료 모델 목록은 실제 사용 가능 상태와 다를 수 있다. OpenCode는 할당 소진 메시지를, Cline의 해당 모델은 `model not found`를 반환했다. Cline 공식 CLI는 프로모션 종료 안내로 표시했다.
4. Freeway가 추가할 기능은 외부 경로 연결과 실제 사용 가능 상태를 기준으로 한 선택이다. 한도·프로모션 상태를 무시해 무료 목록만 늘리는 기능은 목표에 맞지 않는다.
5. 브리지의 한도 오류 정규화는 개선 태스크로 연결한다. 공급처 원문과 공통 오류를 함께 보존해야 사용자가 다음 행동을 알 수 있다.
