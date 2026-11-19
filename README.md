# CODEYE

CODEYE는 프로젝트의 코드를 고정된 스냅샷에서 정적 분석하고, 모델이 제시한 후보를 코드 근거와 반례로 검토하는 자체 호스팅 QA 플랫폼입니다. 보고서에서 발생 조건, 예상 영향, 근거 위치와 미검토 범위를 확인할 수 있습니다.

프로젝트 소스를 직접 확보해 검사하는 Native 실행이 기본입니다. Docker·Kubernetes가 없어도 동작합니다.

## 지금 사용할 수 있는 기능

한 사람이 자기 Linux 머신에서 쓰는 개인 모드입니다.

- **프로젝트 등록과 분석.** Git URL과 브랜치, 또는 로컬 경로를 등록하면 입력을 고정한 사본에서 분석합니다. 원본 경로에서는 아무것도 실행하지 않습니다.
- **환경·문서 자동 파악.** 모듈, 런타임·도구 조건, README와 하위 지침을 찾아 근거와 함께 기록합니다.
- **정적 분석.** Go, C/C++, Python, JS/TS, Rust 분석기와 구조·중복 지표, 비밀정보 검사를 실행합니다. 설치하지 않은 도구는 0건 통과가 아니라 미지원으로 표시합니다.
- **모델 검토.** 전송을 승인한 소스에 한해 독립 조사와 반증 검토를 수행합니다. 모델 없이도 도구 결과 보고서가 나옵니다.
- **보고서.** 웹에서 읽고 같은 리비전을 JSON·Markdown·HTML로 내려받습니다. 숫자는 모두 구조화 데이터에서 계산합니다.
- **예약, 진행 표시, 실행 비교와 사람 판정, 저장 영역 상한, 백업·복원.**

빌드한 뒤(아래) PostgreSQL 프로그램만 설치돼 있으면 한 줄로 시작합니다. 데이터베이스를 직접 만들 필요는 없습니다.

```bash
./bin/codeye up ~/src/myproject    # DB·서버·에이전트를 띄우고 분석을 요청한 뒤 로그인된 브라우저를 엽니다
```

설치부터 백업까지의 절차는 [사용 안내서](docs/GUIDE.md)에 있습니다. 여러 사용자, GitHub App과 PR 댓글, 다중 노드, 폐쇄망 배포는 아직 구현하지 않았습니다([다음 작업](docs/ROADMAP.md)).

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
python3 scripts/test_postgres.py --web --nodes --scheduler --personal   # 임시 PostgreSQL과 Chromium을 쓰는 제품 경로
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
