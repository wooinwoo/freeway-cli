# 최종 목표 구조와 현재 개발 틀

기준일: 2026-11-16. 백엔드와 CLI/TUI는 Go로 구성합니다. TUI는 Nekot에서 추린 Bubble Tea 화면이며 **기존 Node.js POC에 임시 연결**했습니다. 최종 Go 백엔드와 API는 여전히 개발 틀입니다. 아래 구조는 저장소에 만든 개발 영역과 담당 경계를 나타냅니다. `.gitkeep`이 있는 영역은 구현 전의 빈 폴더입니다.

## 전체 구조

```text
freeway-cli/
├── README.md                       간단한 프로젝트 소개
├── LICENSE                         MIT 원문
├── THIRD_PARTY_NOTICES.md           제3자 코드·런타임의 적용 범위
├── CONTRIBUTING.md                 개발·기여 방법
├── .gitignore
├── POC/                            기존 Node.js 게이트웨이·임시 CLI 공급처 확장
│   ├── cli.mjs / server.mjs        연결 명령·로컬 게이트웨이
│   ├── src/
│   │   ├── executables.mjs         PATH 기반 실행 파일 탐색
│   │   ├── opencode.mjs            OpenCode 공식 서버·무료 모델·세션
│   │   ├── cline.mjs               Cline 공식 ACP·무료 카탈로그·취소
│   │   └── provider-bridges.mjs     CLI 공급처별 실행·상태 선택
│   └── tests/                      무료 필터·프로토콜·취소·한도 fixture 검사
├── api/
│   └── openapi.yaml                B: HTTP·SSE 통신 계약의 틀
├── backend/
│   ├── go.mod                      Go 모듈과 최소 버전
│   ├── cmd/
│   │   └── freewayd/
│   │       └── main.go             B: 실행 진입점의 틀
│   ├── internal/
│   │   ├── app/                    B: 구성 조립·설정·시작·종료·잠금
│   │   ├── contracts/              B: 업체·코어 간 Go 인터페이스
│   │   ├── api/                    B: HTTP·SSE 요청 처리
│   │   ├── mcp/                    B: MCP stdio와 로컬 API 중계
│   │   ├── router/                 B: 선택·실행·전환·취소
│   │   ├── quota/                  B: 한도·예약·정산·리셋
│   │   ├── session/                B: 대화·실행 상태
│   │   ├── auth/                   B: 로컬 인증·비밀 저장
│   │   ├── storage/                B: 영속화·데이터 이전·복구
│   │   └── providers/              A: 업체 API·CLI·OAuth·모델·한도 관측
│   └── tests/
│       └── integration/            B: 내부 계층 통합 검사
├── cli/
│   ├── README.md                   CLI 구현 상태와 경계
│   ├── go.mod / go.sum             Go TUI 모듈·의존성 고정
│   ├── cmd/freeway/main.go         A: TUI 실행 진입점
│   ├── commands/                   A: 명령·백엔드 연결·시작
│   ├── client/                     A: 임시 POC 어댑터, 이후 최종 API 클라이언트로 교체
│   │   ├── poc.go                  Node 브리지 실행·스트림·취소
│   │   ├── bridge.mjs              기존 POC 클라이언트 재사용
│   │   └── poc_test.go             무료 필터·문맥·스트림·취소 fixture 검사
│   └── tui/                        A: Nekot에서 추린 화면
│       ├── model.go                대화·하단 입력·모델 메뉴·메모리 상태
│       ├── gateway.go              어댑터 경계·비동기 요청·응답·취소
│       ├── gateway_test.go         문맥 보존·취소 뒤 지연 이벤트 검사
│       ├── models-list.go          샘플 모델 검색·선택
│       ├── prompt-pane.go          여러 줄 입력
│       ├── styles.go               Groovebox 색상
│       ├── model_test.go           입력·초안 보존·모델 메뉴·창 크기 검사
│       └── LICENSE                 Nekot 원본 MIT 고지
├── tests/
│   ├── e2e/                        A: 실행 파일을 사용하는 사용자 흐름
│   └── install/                    A: 설치·업데이트·제거
├── scripts/
│   ├── package/                    A: CLI·백엔드 패키징
│   ├── install/                    A: 설치·버전 교체
│   └── release/                    B: 버전·체크섬·릴리스
├── .github/
│   └── workflows/                  B: 자동 검사·빌드·배포
└── docs/
    ├── user/                       A: 사용자 문서
    ├── api/                        B: API 설명
    ├── development/                B: 공통 개발 규칙, A: 담당 영역 보완
    ├── planning/                   공개 기획·설계·분업
    └── research/                   기존 프로젝트 소스 분석
```

`cli/`는 별도 Go 모듈이며 Bubble Tea·Bubbles·Lip Gloss를 사용합니다. `commands/`는 비어 있습니다. Go 서버·설치기·CI는 개발 틀입니다. 기본 TUI는 임시 POC 어댑터를 사용하며 `--preview`에서만 샘플 모델을 표시합니다.

## 임시 실행 경로

```text
Go TUI
  └─ cli/client/poc.go
      └─ 내장 Node 브리지 (표준입력 JSON / 표준출력 이벤트)
          └─ 기존 POC/src/client.mjs
              └─ 사용자 PC의 기존 POC 게이트웨이
                  └─ 기존 무료 필터·라우팅·공급처
```

이 연결은 TUI에서 실제 무료 모델을 사용하기 위한 임시 구현입니다. 최종 Go 서버를 대신 완성한 것이 아니며, POC의 내부 통신 형식을 최종 HTTP 계약으로 채택하지 않습니다. 모델·스트림·대화 저장 어댑터를 교체하고 TUI를 유지하는 경계를 둡니다. `backend/`와 `api/openapi.yaml`은 이번 연결로 변경하지 않습니다. Go TUI의 대화·초안은 기존 POC의 암호화된 대화 API로 저장하며, 비밀 저장 파일을 직접 열지 않습니다. 기존 POC와 같은 revision 충돌 검사를 사용합니다.

현재 공급처 준비·백엔드 호출·모델 ID·오류 처리와 Go 이식에 필요한 원본 코드 위치는 [백엔드 연동 가이드](../../backend/internal/providers/INTEGRATION.md)에 정리합니다. 업체별 프로토콜과 실행 명령은 [공급처 안내](../../backend/internal/providers/README.md)를 따릅니다.

`backend/internal/providers/client.go`는 현재 백엔드에서 import할 수 있는 Go 공급처 클라이언트입니다. `bridge.mjs`를 통해 같은 POC 게이트웨이의 인증·모델·한도·추론을 재사용하며, 독립적인 Go 서버·비밀 저장소·원장을 추가하지 않습니다. `examples/basic/main.go`에서 실제 호출할 수 있습니다. 최종 공통 Go 계약과 `freewayd` 구현은 여전히 별도 작업입니다.

임시 공급처는 Kilo API, OpenCode CLI, Cline CLI입니다. POC 라우터가 모델의 무료 상태와 공급처 가용성을 판단합니다. CLI 계층은 사용자 PC의 공식 실행 파일만 호출하며, 최종 `backend/internal/providers/`에 구현이 완료됐다는 뜻은 아닙니다. Cline은 무료 카탈로그 확인 후 ACP 모델 선택 응답을 받은 뒤에만 프롬프트를 전달합니다. OpenCode는 무료 할당량 소진을 429·cooldown으로 처리합니다.

## 실행과 통신 경계

목표 실행 모델은 사용자의 PC에 있는 게이트웨이 한 개입니다. CLI와 외부 클라이언트가 이 게이트웨이에 요청합니다. 게이트웨이는 연결된 외부 API, 공식 CLI 프로세스, 별도 로컬 모델 서버를 사용할 수 있습니다. 로컬 모델 가중치나 추론 엔진을 이 저장소에 포함한다는 계획은 아닙니다.

- `api/openapi.yaml`은 HTTP 요청·응답과 스트림 이벤트의 통신 계약입니다. 현재는 `paths: {}`인 빈 틀이며 실행 가능한 API를 약속하지 않습니다.
- `backend/internal/contracts/`는 Go 내부의 모델·이벤트·공급처 인터페이스를 위한 영역입니다. HTTP 처리기나 CLI 구현에 의존하지 않습니다.
- `backend/internal/api/`가 통신 형식과 실행 정책을 연결합니다. CLI는 이 API를 사용하며 Go 내부 패키지를 직접 import하지 않습니다.
- 향후 MCP는 같은 `freewayd` 실행 파일의 별도 `mcp` 모드에서 표준입출력을 처리하고, 인증된 로컬 API로 중계합니다. MCP 중계 프로세스가 별도의 상태 DB나 쿼터 원장을 열지 않습니다. stdout은 MCP 메시지 전용이며 로그는 stderr로 보냅니다.
- CLI와 MCP 모드가 백엔드 시작을 요청할 때는 같은 daemon 발견·시작 규약을 사용합니다. `app/`이 단일 작성자 잠금을 먼저 얻은 뒤 저장소·인증정보·상태를 초기화합니다.

현재 `main.go`는 미구현 안내 후 실패로 종료합니다. 서버 시작, 포트 열기, 로그인, 공급처 호출이나 MCP 메시지 처리는 하지 않습니다.

## 의존성과 책임

`app/`이 저장소·인증·공급처·쿼터·세션·라우터·API를 조립합니다. 공통 계약은 상위 구현을 역으로 import하지 않습니다. 필요한 인터페이스는 실제 기능을 구현할 때 최소한으로 추가합니다.

| 책임 | A | B |
| --- | --- | --- |
| 업체 연결 | API·공식 CLI 실행·OAuth·refresh·모델 조회·오류 및 한도 응답 파싱 | 정규화된 어댑터 계약과 공통 비밀 저장 API 제공 |
| 한도와 라우팅 | 관측 범위·출처·시각·리셋 의미 전달 | 예약·확정·취소·정산·선택·전환·복구 |
| 사용자 흐름 | 명령·화면·연결·설정·선택 이유 표시 | 실행·상태·오류·취소 API 제공 |
| 데이터 | 저장 API 사용, 자체 상태 파일을 만들지 않음 | 비밀·대화·사용량 저장, 잠금·마이그레이션·복구 |
| 출시 | 포함 파일·패키징·설치·사용자 문서 | CI·버전·체크섬·릴리스·API/공통 개발 문서 |

공급처 어댑터는 다음 공급처를 선택하지 않습니다. CLI는 공급처나 원장을 직접 호출하지 않습니다. 비용·기능·가용성에 따른 선택과 실행·취소의 조정은 `router/`에서 담당합니다. 쿼터의 예약·정산 규칙은 `quota/`, 디스크 영속성은 `storage/`에서 담당합니다.

## 검사와 데이터 위치

Go 단위 테스트는 소스 옆의 `*_test.go`에 둡니다. 내부 패키지 통합 검사는 같은 Go 모듈의 `backend/tests/integration/`에 둡니다. 루트의 `tests/e2e/`와 `tests/install/`은 실행 파일을 외부에서 다루는 검사 영역이며, 백엔드의 `go test ./...`에 포함되지 않습니다.

실제 사용자 키·토큰·대화·DB는 버전 관리 밖의 OS 사용자 데이터 위치를 사용하도록 구현합니다. 저장소에는 코드·계약·비밀 없는 fixture·문서를 둡니다. 기존 POC의 `.data/` 위치와 새 저장소 이전 규약은 별도로 다루며 원본 데이터를 자동 변경하지 않습니다.

## 기존 문서와 관계

이 문서는 이전 [핵심 기획의 폴더 구조](../planning/18-brief.md)를 Go 백엔드 기준으로 갱신한 기준입니다. 72개 태스크는 전체를 즉시 구현한다는 약속이 아니며, 먼저 두 경로의 가치와 연결 계약을 검증합니다. [기존 도구와 겹치는 기능](../research/README.md)도 구현 범위를 정하는 기준으로 유지합니다.

근거: [Go 서버 구조](https://go.dev/doc/modules/layout), [Go internal 접근 규칙](https://pkg.go.dev/cmd/go#hdr-Internal_Directories), [Go 모듈과 go.sum](https://go.dev/doc/modules/managing-source), [MCP 전송 규격](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports).
