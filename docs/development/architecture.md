# 구조와 책임

네이티브 제품은 Go TUI, 로컬 게이트웨이, 공급처 도구로 구성됩니다. 문서 사이트는 Astro + Starlight로 빌드한 정적 파일입니다. 기존 Node.js POC는 별도로 보관합니다.

## 실행 경계

```text
Go TUI
  cli/client (HTTP + SSE, Bearer 인증)
    로컬 Go 게이트웨이 API v1
      라우팅·한도·대화 저장·로컬 인증
        backend/internal/providers (업체 HTTP·공식 CLI)
```

`freeway providers`는 패키지의 독립 공급처 도구를 호출합니다. 이 경로는 인증·모델 조회·진단용입니다. TUI가 공급처 내부 패키지나 인증 파일을 직접 읽지 않습니다.

## 현재 파일

| 경로 | 책임 |
| --- | --- |
| `cli/cmd/freeway` | 실행 옵션, TUI 시작, 공급처·업데이트 명령 |
| `cli/client` | loopback 인증 HTTP·SSE, 대화 저장·revision·프롬프트 기록 |
| `cli/tui` | 입력·첨부·대화·보관함·내보내기·비동기 이벤트 경계 |
| `cli/update` | 검증·불변 버전 설치·활성 전환·롤백·제거 |
| `cli/cmd/package` | CLI·게이트웨이·공급처 도구·라이선스·manifest·SHA256SUMS 패키징 |
| `backend/cmd/freewayd` | 로컬 게이트웨이 실행 및 클라이언트 키 조회 |
| `backend/internal/providers` | 9개 HTTP API, OpenCode V1·Cline ACP, 인증·오류·한도 관측 |
| `backend/cmd/providerctl` | 독립 네이티브 인증·모델·호출·공식 CLI 설치 도구 |
| `api/openapi.yaml` | HTTP·SSE API v1 계약 (JSON 구문은 유효한 YAML) |
| `docs/site` | 영문·한글 정적 문서·검색·사용법 |
| `tests/e2e/native_smoke.py` | 실제 배포 실행 파일의 격리된 설치·API·제거 검사 |
| `POC` | 기존 Node.js 구현 보관 |

## 백엔드의 책임

- `app/`: 단일 작성자 잠금, 설정·수명주기·종료, 구성 조립.
- `contracts/`: 공급처와 코어의 내부 Go 계약.
- `api/`: API v1·인증·입력 검증·SSE·연결 종료 시 취소.
- `router/`: 공급처 선택·무료 조건·실행·폴백·취소의 일관성.
- `quota/`: 한도 관측·예약·정산·리셋·중복 실행 방지.
- `session/`: 대화·실행 상태와 revision 충돌.
- `auth/`, `storage/`: 비밀·대화·사용량 영속화, 암호화·마이그레이션·복구.
- 향후 `mcp/`: 인증된 공통 로컬 API 중계. 별도 원장이나 DB를 열지 않음.

`freeway`는 같은 데이터 디렉터리를 쓰는 로컬 게이트웨이가 있으면 연결하고, 없으면 설치 버전에 포함된 `freewayd`를 시작합니다. 자동 시작한 서버는 루프백의 가용 포트와 프로세스별 연결 키를 사용하며 마지막 클라이언트가 닫힌 뒤 종료합니다. 고정 주소가 필요하면 `freeway gateway serve`를 사용합니다. 대화와 사용량은 전용 디렉터리에 암호화해 저장합니다.

## 데이터와 배포

제품은 `~/.freeway/app` 또는 사용자가 지정한 전용 경로의 버전 디렉터리에 설치합니다. 계정·대화 데이터는 프로그램 디렉터리 밖에 둡니다. 제거는 프로그램만 삭제합니다. POC 데이터를 자동 이전하거나 삭제하지 않습니다.

업데이터는 아카이브 SHA256과 내부 파일·플랫폼·API 버전·실행 버전을 검증한 뒤 활성 버전을 바꿉니다. 실패하면 기존 활성 버전을 유지합니다. 신뢰한 릴리스 명세가 배포 신뢰의 기준이며, SHA256만으로 배포자의 신원을 증명하지 않습니다.

문서 빌드에는 Node.js가 필요하지만 출력은 HTML·CSS·JS·검색 인덱스뿐입니다. 정적 호스팅만으로 동작합니다. 사이트·릴리스의 외부 공개와 CI 운영은 별도 작업입니다.

[완료 조건과 인계](client-delivery.md) · [공급처 계약](../../backend/internal/providers/API.md) · [개발 참여](../../CONTRIBUTING.md)
