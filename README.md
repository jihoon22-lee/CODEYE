# CODEYE

CODEYE는 프로젝트의 문서와 코드를 함께 읽는 자체 호스팅 QA 플랫폼입니다. 보고서에서 문서에 적힌 동작이 코드와 일치하는지, 유지할 만한 구현이 무엇인지, 어떤 조건에서 문제가 생기는지 확인할 수 있습니다. 각 판단에는 분석 당시의 문서·코드 근거가 연결됩니다.

프로젝트 소스를 직접 확보해 검사하는 Native 실행이 기본입니다. Docker·Kubernetes가 없어도 동작합니다.

## 다운로드하고 실행하기

Linux(WSL2 포함)에서 일반 Linux 계정으로 실행합니다. 기본 설치에는 PostgreSQL 14 이상의 서버 프로그램이 필요합니다. CODEYE가 자체 데이터베이스를 만들고 시작합니다. 배포 파일에 실행 파일과 웹 화면이 들어 있습니다. [준비물과 지원 범위](docs/GUIDE.md#준비물)

| Linux CPU | CODEYE v0.1.0 | 검증 파일 |
|---|---|---|
| x86-64 (`amd64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-amd64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-amd64.tar.gz.sha256) |
| ARM64 (`arm64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-arm64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-arm64.tar.gz.sha256) |

x86-64 Linux에서 내려받아 시작하는 예입니다. ARM64에서는 파일명과 폴더명의 `amd64`를 `arm64`로 바꿉니다.

```sh
curl -fLO https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-amd64.tar.gz &&
  curl -fLO https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.0/codeye-v0.1.0-linux-amd64.tar.gz.sha256 &&
  sha256sum -c codeye-v0.1.0-linux-amd64.tar.gz.sha256 &&
  tar -xzf codeye-v0.1.0-linux-amd64.tar.gz &&
  cd codeye-v0.1.0-linux-amd64 &&
  ./codeye up
```

`up`은 관리자 로그인 링크를 열고 터미널에도 출력합니다. 화면의 **폴더 경로 또는 Git 주소**에 서버가 읽을 수 있는 절대 경로 또는 GitHub 저장소·PR 주소를 넣고 **분석**을 누릅니다. 다시 열 때는 `./codeye open`을 실행합니다. [첫 시작과 프로젝트 등록](docs/GUIDE.md#첫-시작)

Git 저장소를 분석하려면 `git`을 설치합니다. 비공개 저장소에는 운영자가 연결한 플랫폼 GitHub 계정에 읽기 권한을 부여합니다. 사용자 가입과 프로젝트 공유는 CODEYE의 웹 계정에서 관리합니다. [플랫폼 GitHub 연결](docs/GITHUB.md) · [다른 사람 초대](docs/GUIDE.md#다른-사람-초대)

모델 검토를 사용하려면 관리자 화면의 **OpenCode 연결**에서 서버에 설치된 OpenCode를 지정하고 연결을 확인합니다. 모델 설정 전에도 정적 분석을 실행할 수 있습니다. [모델 설정](docs/GUIDE.md#모델-설정)

인터넷이 없는 머신에서는 같은 배포 파일과 검증 파일을 함께 옮깁니다. 묶음 안 `INSTALL.md`의 오프라인 절차를 따릅니다. [폐쇄망 배포팩](docs/OPERATIONS.md#폐쇄망-배포팩)

## 지금 사용할 수 있는 기능

- **프로젝트 등록과 분석.** Git 주소와 브랜치, pull request 주소, 또는 서버 폴더 경로를 넣으면 입력을 고정한 사본에서 분석합니다.
- **환경·문서 자동 파악.** 모듈, 런타임·도구 조건, README와 하위 지침을 찾아 근거와 함께 기록합니다.
- **정적 분석.** Go, C/C++, Python, JS/TS, Rust 분석기와 구조·중복 지표, 비밀정보 검사를 실행합니다. 설치하지 않은 도구는 0건 통과가 아니라 미지원으로 표시합니다.
- **모델 검토.** 전송을 허용한 소스에 한해 독립 조사와 반증 검토를 수행합니다. 모델 없이도 도구 결과 보고서가 나옵니다.
- **보고서.** 웹에서 읽고 같은 리비전을 JSON·Markdown·HTML로 내려받습니다. 종합에서 조치할 부분을 확인하고, 문서·코드 분석에서 해당 파일과 줄을 펼쳐볼 수 있습니다.
- **GitHub pull request.** 바뀐 줄 기준으로 분석하고, 원하면 결과 댓글 하나를 남겨 다시 분석할 때 고칩니다. PR 갱신이나 플랫폼 계정 멘션으로도 실행할 수 있습니다. [저장소 웹훅 설정](docs/GITHUB.md#저장소-웹훅)
- **여러 사용자와 권한, 프로젝트 구성원, 감사 기록.**
- **예약, 진행 표시, 실행 비교와 사람 판정, 저장 영역 상한, 백업·복원, 오프라인 설치, 여러 노드.**

소스에서 빌드한 개발판에서는 Go 패키지를 직접 사용하는 코드·테스트와 C/C++ 진단에 딸린 관련 위치를 확인할 수 있습니다. **테스트 포함 분석**을 선택하면 Linux 노드가 작업 사본에서 프로젝트의 기존 테스트를 실행하고 보고서에 결과를 남깁니다. [테스트 범위와 실행 조건](docs/EXECUTION.md#기존-테스트-실행)

## 빌드와 검증

개발 환경은 Go 1.27.4, Node 24.18.0, Python 3.14, PostgreSQL 17입니다.

```bash
go build -o bin/codeye ./cmd/codeye
npm --prefix web ci
npm --prefix web run build
./bin/codeye doctor --format json
```

`doctor`의 종료 코드 1은 미설정·미검증·사용 불가 항목이 있다는 뜻입니다.

```bash
go test ./...                                   # Go 단위 시험
npm --prefix web test                           # 웹 시험
python3 scripts/validate_bundle.py              # 문서·schema·예시 계약
python3 scripts/test_postgres.py --web --nodes --scheduler --instance --web-service   # 임시 PostgreSQL과 Chromium을 쓰는 제품 경로
```

시험별 준비물과 CI 범위는 [수용 시험](docs/TESTING.md)에 있습니다.

## 문서

| 알고 싶은 것 | 문서 |
|---|---|
| 설치하고 쓰는 방법 | [사용 안내서](docs/GUIDE.md) · [운영](docs/OPERATIONS.md) |
| 무엇을 만드는 제품인가 | [제품 범위와 요구사항](docs/REQUIREMENTS.md) · [다음 작업](docs/ROADMAP.md) · [변경 이력](CHANGELOG.md) |
| 어떻게 동작하는가 | [아키텍처](docs/ARCHITECTURE.md) · [실행·노드·스케줄링](docs/EXECUTION.md) · [API](docs/API.md) |
| 분석과 결과 | [분석기](docs/ANALYZERS.md) · [모델 검토](docs/REVIEW.md) · [보고서·화면](docs/REPORT.md) · [품질 평가](docs/EVALUATION.md) |
| 보안과 연동 | [보안](docs/SECURITY.md) · [GitHub 연동](docs/GITHUB.md) |
| 개발 규칙과 근거 | [개발 에이전트 지침](AGENTS.md) · [수용 시험](docs/TESTING.md) · [결정 기록](docs/DECISIONS.md) |

기계 판독 계약은 [api/openapi.yaml](api/openapi.yaml)과 [schemas](schemas), 입력·출력 예시는 [examples](examples)에 있습니다. 라이선스는 소유자가 결정합니다.
