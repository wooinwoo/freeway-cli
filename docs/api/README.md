# API 참고

기준 계약은 [`api/openapi.yaml`](../../api/openapi.yaml)입니다. 현재 파일에는 엔드포인트가 없는 개발용 틀만 있습니다.

첫 API 구현 시 요청·응답, SSE 이벤트, 종료·오류·취소, 로컬 인증과 API 버전을 함께 정의합니다. 기존 [`POC/server.mjs`](../../POC/server.mjs)의 동작을 새 Go 서버가 이미 제공한다고 가정하지 않습니다.
