# CODEYE

CODEYE는 프로젝트의 문서와 코드를 함께 분석하는 자체 호스팅 QA 플랫폼입니다. 문서에 적힌 동작이 코드와 일치하는지, 어떤 조건에서 문제가 생기는지 분석 당시의 문서와 코드 근거를 보며 판단할 수 있습니다. 프로젝트를 별도 작업 사본에서 분석하며 Linux에서 Docker 없이 실행합니다.

## 다운로드하고 실행하기

Linux(WSL2 포함)의 일반 계정에서 실행합니다. 기본 설치에는 PostgreSQL 14 이상 서버 프로그램이 필요합니다. 배포 파일에는 CODEYE 실행 파일과 웹 화면이 들어 있습니다. [준비물과 지원 범위](docs/GUIDE.md#준비물)

| Linux CPU | CODEYE v0.1.3 | 검증 파일 |
|---|---|---|
| x86-64 (`amd64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-amd64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-amd64.tar.gz.sha256) |
| ARM64 (`arm64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-arm64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-arm64.tar.gz.sha256) |

**이전 배포판을 사용 중이라면** 기존 계정·프로젝트·보고서를 이어서 쓰도록 설정과 상태 디렉터리를 유지하면서 실행 파일과 웹 화면을 함께 교체합니다. [백업과 배포판 업그레이드](docs/OPERATIONS.md#배포판-업그레이드)

처음 설치할 때는 배포 파일과 검증 파일을 같은 폴더에 받습니다. 아래는 x86-64 예시입니다. ARM64는 `amd64`를 `arm64`로 바꿉니다.

```sh
sha256sum -c codeye-v0.1.3-linux-amd64.tar.gz.sha256 &&
  tar --keep-old-files -xzf codeye-v0.1.3-linux-amd64.tar.gz &&
  cd codeye-v0.1.3-linux-amd64 &&
  sha256sum -c SHA256SUMS &&
  ./codeye up
```

`up`이 연 관리자 화면에서 **폴더 경로 또는 Git 주소**에 서비스 계정이 읽을 수 있는 서버 폴더, GitHub 저장소 또는 PR 주소를 넣고 **분석**을 누릅니다. 브라우저가 열리지 않으면 터미널의 로그인 링크를 엽니다. 새 링크는 `./codeye open`으로 받습니다. [첫 시작과 프로젝트 등록](docs/GUIDE.md#첫-시작)

- Git 저장소는 `git`이 필요합니다. 비공개 저장소는 플랫폼 GitHub 계정에 읽기 권한을 주고, 프로젝트와 보고서 공유는 CODEYE 웹 계정의 구성원 설정에서 관리합니다. [플랫폼 GitHub 계정](docs/GITHUB.md#플랫폼-github-계정) · [다른 사람 초대](docs/GUIDE.md#다른-사람-초대)
- 관리자는 **관리 > 언어 도구 경로**에서 서비스 머신의 Python, JDK, Go, Rust(Cargo), Node.js를 선택할 수 있습니다. 지정한 경로는 다음 로컬 분석부터 적용됩니다. [언어 도구 설정](docs/GUIDE.md#언어-도구-경로) · [본인 이메일로 관리자 로그인](docs/GUIDE.md#첫-관리자-로그인)
- 모델 검토를 쓰려면 **관리 > OpenCode 연결**에서 서버의 OpenCode를 설정하고 연결을 확인합니다. 모델 설정 전에도 정적 분석을 실행할 수 있습니다. [모델 설정](docs/GUIDE.md#모델-설정)
- 인터넷이 없는 머신에는 배포 파일과 검증 파일을 함께 옮기고 압축을 푼 폴더의 `INSTALL.md`를 따릅니다. [폐쇄망 배포팩](docs/OPERATIONS.md#폐쇄망-배포팩)

## 분석 결과 읽기

| 확인할 내용 | 보고서에서 보는 방법 |
|---|---|
| 문서대로 동작하는가 | 문서에서 동작을 선택하고 구현 분기, 호출한 함수, 테스트 단언을 펼칩니다. 확인된 조건과 근거가 부족한 조건을 따로 볼 수 있습니다. [모델 검토](docs/REVIEW.md) |
| 어떤 코드부터 살펴볼까 | 종합에서 우선 항목을 고릅니다. 정적 분석에서는 실행한 도구, 규칙의 의미, 해당 파일과 줄을 확인합니다. [보고서 읽기](docs/GUIDE.md#보고서-읽기) |
| 기존 테스트 결과는 어떤가 | **테스트 포함 분석**을 선택합니다. 성공·실패·건너뜀 개수와 확인된 실패 위치가 보고서에 표시됩니다. [언어별 분석과 테스트](docs/GUIDE.md#언어별-분석과-테스트) |

Java, Go, Python, JavaScript·TypeScript, C/C++, Rust를 분석합니다. Java는 Maven·Gradle 모듈과 JDK 요구 버전을 찾고, PMD로 소스를, SpotBugs로 이번 분석에서 컴파일한 클래스를 검사합니다. [분석기 목록](docs/ANALYZERS.md#분석기-목록)

프로젝트 구성원을 추가해 결과를 공유하거나 정기 분석을 예약할 수 있습니다. GitHub PR은 고정한 커밋을 분석하며, 설정에 따라 댓글 멘션으로 호출하거나 결과를 댓글로 받을 수 있습니다. 보고서는 웹에서 읽거나 HTML·Markdown·JSON으로 내려받습니다. [사용 안내서](docs/GUIDE.md) · [GitHub 연동](docs/GITHUB.md) · [v0.1.3 변경 이력](CHANGELOG.md)

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
