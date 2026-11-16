# 제3자 구성요소와 고지 범위

Freeway 자체 코드와 문서의 기본 라이선스는 [MIT](LICENSE)입니다. 제3자 자료의 원래 조건을 바꾸지 않습니다. 선택 근거와 재사용 기준은 [라이선스 문서](docs/development/licensing.md)에 있습니다.

## 현재 개발 틀

| 항목 | 현재 저장소에 포함한 범위 | 처리 기준 |
| --- | --- | --- |
| Go | 표준 라이브러리를 사용하는 새 진입점. Go 도구나 바이너리를 포함하지 않음 | 실행 파일 배포 시 해당 Go 버전의 라이선스·저작권 고지 포함 |
| Node.js | 기존 POC의 실행 환경. 런타임 파일을 포함하지 않음 | 런타임을 묶는 배포에는 해당 버전의 LICENSE와 포함 구성요소 고지 보존 |
| 외부 Go 모듈 | CLI의 Bubble Tea·Bubbles·Lip Gloss 및 전이 의존성 | 버전은 cli/go.mod·go.sum으로 고정하며 실행 파일 배포 시 해당 모듈의 원문 고지를 포함 |
| 외부 npm 의존성 | POC/package.json에 선언된 항목 없음 | 의존성 추가 또는 배포 파일 포함 시 다시 확인 |
| 조사 프로젝트 | 원본 URL·고정 커밋을 인용한 분석 문서 | 클론 저장소와 구현 소스는 배포에 포함하지 않음 |

Go 라이선스 원문은 [Go 저장소](https://go.dev/LICENSE), Node.js 고지는 [Node.js 저장소](https://github.com/nodejs/node/blob/main/LICENSE)를 참고합니다. 실제 배포에서는 링크만 두지 않고 사용한 버전에 필요한 원문 고지를 포함합니다.

기존 v0.5.0 릴리스 파일 전체의 구성과 고지는 이번 개발 틀 작업에서 다시 검증하지 않았습니다. `POC/install.ps1`은 그 기존 릴리스를 가리키며 이번 작업으로 새 설치 패키지를 발행하지 않습니다.

## Nekot TUI에서 가져온 코드

- 원본: https://github.com/BalanceBalls/nekot
- 고정 커밋: `4cfd8282e80145bb35b60456dc6fa624b351cd3f`.
- 라이선스: MIT. 원 저작권과 허가문은 [`cli/tui/LICENSE`](cli/tui/LICENSE)에 그대로 보존합니다.
- `components/models-list.go`를 `cli/tui/models-list.go`로 축소했습니다. 필터·키보드·휠 탐색을 유지하고, 마우스 영역 라이브러리·공급처 데이터·공유 전역 스타일을 제거했습니다.
- `panes/prompt-pane.go`의 textarea 초기화와 스타일을 `cli/tui/prompt-pane.go`로 추렸습니다. 파일·이미지 첨부, 클립보드 이미지 처리, 외부 설정 의존성을 제거했습니다.
- `util/shared-colors.go`의 Groovebox 팔레트를 `cli/tui/styles.go`로 추렸습니다. 원본 패널 테두리·다중 테마·Markdown 렌더러는 포함하지 않습니다.
- `cli/tui/model.go`는 이 UI를 연결하는 새 화면 상태입니다. 원본 DB·업체 클라이언트·세션 오케스트레이터·웹 검색·제목 생성 코드는 가져오지 않았습니다.

직접 사용하는 UI 모듈은 `charm.land/bubbles/v2 v2.1.1`, `charm.land/bubbletea/v2 v2.0.8`, `charm.land/lipgloss/v2 v2.0.5`이며 각각 MIT입니다. 모듈 소스를 저장소에 vendor하지 않습니다. 실행 파일을 배포할 때에는 전이 의존성까지 포함한 라이선스 묶음이 필요하며, 현재 작업은 소스와 로컬 실행 미리보기만 제공합니다.

## 외부 코드가 추가되면 남길 정보

원 프로젝트·원본 파일·버전 또는 커밋, SPDX 식별자, 보존할 LICENSE/NOTICE 위치, 수정 내용, 실제 배포 묶음에 포함할 고지를 기록합니다. 타인의 기존 저작권 표시를 이 프로젝트 명의로 대체하지 않습니다.
