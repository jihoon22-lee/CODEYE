# CODEYE

CODEYE는 프로젝트의 문서와 코드를 함께 분석하는 자체 호스팅 QA 플랫폼입니다. 문서에 적힌 동작이 코드와 일치하는지, 어떤 조건에서 문제가 생기는지 분석 당시의 문서와 코드 근거를 보며 판단할 수 있습니다. 프로젝트를 별도 작업 사본에서 분석하며 Linux에서 Docker 없이 실행합니다.

## 다운로드하고 실행하기

Linux(WSL2 포함)의 일반 계정에서 실행합니다. 기본 설치에는 PostgreSQL 14 이상 서버 프로그램이 필요합니다. 배포 파일에는 CODEYE 실행 파일과 웹 화면이 들어 있습니다. [준비물과 지원 범위](docs/GUIDE.md#준비물)

| Linux CPU | CODEYE v0.1.1 | 검증 파일 |
|---|---|---|
| x86-64 (`amd64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.1/codeye-v0.1.1-linux-amd64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.1/codeye-v0.1.1-linux-amd64.tar.gz.sha256) |
| ARM64 (`arm64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.1/codeye-v0.1.1-linux-arm64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.1/codeye-v0.1.1-linux-arm64.tar.gz.sha256) |

**v0.1.0을 사용 중이라면** 기존 계정·프로젝트·보고서를 이어서 쓰도록 설정과 상태 디렉터리를 유지하면서 실행 파일과 웹 화면을 함께 교체합니다. [백업과 배포판 업그레이드](docs/OPERATIONS.md#배포판-업그레이드)

처음 설치할 때는 배포 파일과 검증 파일을 같은 폴더에 받습니다. 아래는 x86-64 예시입니다. ARM64는 `amd64`를 `arm64`로 바꿉니다.

```sh
sha256sum -c codeye-v0.1.1-linux-amd64.tar.gz.sha256 &&
  tar --keep-old-files -xzf codeye-v0.1.1-linux-amd64.tar.gz &&
  cd codeye-v0.1.1-linux-amd64 &&
  sha256sum -c SHA256SUMS &&
  ./codeye up
```

`up`이 연 관리자 화면에서 **폴더 경로 또는 Git 주소**에 서비스 계정이 읽을 수 있는 서버 폴더, GitHub 저장소 또는 PR 주소를 넣고 **분석**을 누릅니다. 브라우저가 열리지 않으면 터미널의 로그인 링크를 엽니다. 새 링크는 `./codeye open`으로 받습니다. [첫 시작과 프로젝트 등록](docs/GUIDE.md#첫-시작)

- Git 저장소는 `git`이 필요합니다. 비공개 저장소는 플랫폼 GitHub 계정에 읽기 권한을 주고, 프로젝트와 보고서 공유는 CODEYE 웹 계정의 구성원 설정에서 관리합니다. [플랫폼 GitHub 계정](docs/GITHUB.md#플랫폼-github-계정) · [다른 사람 초대](docs/GUIDE.md#다른-사람-초대)
- 모델 검토를 쓰려면 **관리 > OpenCode 연결**에서 서버의 OpenCode를 설정하고 연결을 확인합니다. 모델 설정 전에도 정적 분석을 실행할 수 있습니다. [모델 설정](docs/GUIDE.md#모델-설정)
- 인터넷이 없는 머신에는 배포 파일과 검증 파일을 함께 옮기고 압축을 푼 폴더의 `INSTALL.md`를 따릅니다. [폐쇄망 배포팩](docs/OPERATIONS.md#폐쇄망-배포팩)

## 지금 사용할 수 있는 기능

- Go, C/C++, Python, JS/TS, Rust 프로젝트의 환경과 문서를 자동으로 찾고, 설치된 정적 분석기로 검사합니다. Go 패키지를 직접 사용하는 코드·테스트와 C/C++ 진단의 관련 위치를 해당 코드 줄에서 확인할 수 있습니다. [분석기](docs/ANALYZERS.md)
- **테스트 포함 분석**은 Linux에서 작업 사본과 전용 loopback 네트워크를 사용해 지원되는 기존 테스트를 실행합니다. 보고서에 실행 결과와 확인된 Go 테스트 실패의 선언 위치가 나옵니다. [실행 조건](docs/EXECUTION.md#기존-테스트-실행) · [보고서 읽기](docs/GUIDE.md#보고서-읽기)
- 보고서의 종합에서 분석 상태와 공백을 보고 문서 대조, 도구 진단, 모델이 검토한 후보의 근거를 펼쳐볼 수 있습니다. 같은 보고서를 JSON·Markdown·HTML로 내보낼 수 있습니다. [보고서·화면](docs/REPORT.md)
- GitHub PR은 고정한 커밋의 변경 파일부터 분석합니다. 설정에 따라 결과 댓글을 게시하거나 PR 이벤트·댓글 멘션으로 분석을 시작할 수 있습니다. [GitHub 연동](docs/GITHUB.md)
- 프로젝트 구성원, 예약, 진행 표시, 실행 비교와 사람 판정, 백업·복원과 여러 노드 운영을 지원합니다. [사용 안내서](docs/GUIDE.md) · [운영](docs/OPERATIONS.md)

## 빌드와 검증

```bash
go build -o bin/codeye ./cmd/codeye
npm --prefix web ci
npm --prefix web run build
./bin/codeye doctor --format json
```

`doctor`의 종료 코드 1은 미설정·미검증·사용 불가 항목이 있다는 뜻입니다. 시험별 준비물과 CI 범위는 [수용 시험](docs/TESTING.md#시험-실행-방법)에 있습니다.

```bash
go test ./...
npm --prefix web test
python3 scripts/validate_bundle.py
```

## 문서

| 알고 싶은 것 | 문서 |
|---|---|
| 설치하고 쓰는 방법 | [사용 안내서](docs/GUIDE.md) · [운영](docs/OPERATIONS.md) |
| 제품 범위와 계획 | [요구사항](docs/REQUIREMENTS.md) · [로드맵](docs/ROADMAP.md) · [변경 이력](CHANGELOG.md) |
| 내부 동작 | [아키텍처](docs/ARCHITECTURE.md) · [실행·노드·스케줄링](docs/EXECUTION.md) · [API](docs/API.md) |
| 분석과 결과 | [분석기](docs/ANALYZERS.md) · [모델 검토](docs/REVIEW.md) · [보고서·화면](docs/REPORT.md) · [품질 평가](docs/EVALUATION.md) |
| 보안과 연동 | [보안](docs/SECURITY.md) · [GitHub 연동](docs/GITHUB.md) |
| 개발 규칙과 근거 | [개발 에이전트 지침](AGENTS.md) · [수용 시험](docs/TESTING.md) · [결정 기록](docs/DECISIONS.md) |

기계 판독 계약과 예시는 [OpenAPI](api/openapi.yaml) · [스키마](schemas) · [입출력 예시](examples)에 있습니다. 라이선스는 소유자가 결정합니다.
