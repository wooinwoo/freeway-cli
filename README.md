# Freeway

여러 서비스에 흩어진 개인의 무료 모델 사용 경로를 연결하는 로컬 게이트웨이 기획과 POC를 관리합니다.

## 저장소 구조

```text
POC/             기존 Node.js 구현과 설치 파일
  src/           공급처 연결, 라우팅, 한도, CLI/MCP
  public/        브라우저 클라이언트
  bin/           Windows 실행 래퍼
  cli.mjs        CLI 진입점
  server.mjs     로컬 게이트웨이 진입점
  package.json   기존 패키지와 실행 명령
  research.json  초기 공급처 조사
  install.ps1    기존 Windows 릴리스 설치 스크립트
docs/
  planning/      기획, 구조, 역할 분담과 검증 기록
  research/      LocalRouter·LiteLLM·CLIProxyAPI 소스 분석
```

## 문서

- [유사 프로젝트 비교와 구조 그림](docs/research/README.md)
- [기획 문서 목차](docs/planning/00-index.md)
- [프리웨이 핵심 기획](docs/planning/18-brief.md)

로컬 게이트웨이와 자동 전환은 기존 프로젝트도 제공합니다. 프리웨이의 독립 구현 범위는 비교 분석을 바탕으로 결정합니다. 기존 기획의 차별화 주장은 최신 소스 분석과 함께 읽어야 합니다.

## 기존 POC 실행

Node.js 22 이상에서 저장소 루트를 기준으로 실행합니다.

```bash
node POC/cli.mjs --help
npm --prefix POC start
```

게이트웨이와 모델 요청을 사용하려면 별도로 공급처 연결과 사용 가능 한도가 필요합니다. 기존 기능·Windows 설치 방법은 [POC 설명](POC/README.md)을 참고하세요. `POC/install.ps1`은 기존 v0.5.0 릴리스 설치용이며 새 설계의 배포물이 아닙니다.

소스 실행의 기본 데이터 위치도 `POC/.data/`로 바뀝니다. 이전에 저장소 루트의 `.data/`를 사용했다면 `FREEWAY_DATA_DIR`에 기존 폴더의 절대경로를 지정하세요. 기존 키·설정·대화 파일은 자동으로 이동하거나 삭제하지 않습니다.

```bash
FREEWAY_DATA_DIR=/absolute/path/to/freeway-cli/.data npm --prefix POC start
```

기존 데이터를 이어 쓰는 CLI에도 같은 `FREEWAY_DATA_DIR` 환경변수를 지정해야 합니다. 위 명령의 경로는 실제 기존 데이터 폴더로 바꿉니다.
