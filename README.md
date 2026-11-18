# CODEYE

**코드를 보는 눈 — AI 기반 심층 코드 품질 분석·검토 플랫폼**

CODEYE는 프로젝트의 소스와 문서를 이해하고, 상호 보완적인 정적 분석 도구와 LLM 검토를 결합하여, 근거와 한계를 확인할 수 있는 QA 보고서를 제공하는 자체 호스팅 플랫폼입니다.

> **R0 개인 모드 검증 완료 · 2026-11-18.** Native 워커가 고정한 Go 스냅샷에서 오프라인 Go vet와 승인된 모델 검토를 수행합니다. 웹에서 발생 조건·근거·미검토 범위를 확인하고 같은 리비전을 JSON·Markdown·HTML로 내보낼 수 있습니다. 합성 전후 10쌍과 공개 CODEYE 사본을 검증했습니다. [R0 결과와 제한](docs/36-r0-pilot.md) · [실행과 노드 등록](docs/28-w06-node-protocol.md) · [워커 자동 증감](docs/30-w07-scheduler.md) · [모델 설정](docs/32-w10-opencode.md) · [보고서 읽기](docs/35-w13-evidence-report.md)

## 제품의 중심

- **분석 지침:** 프로젝트 맥락, 문제 탐색, 호출 경로·방어 코드·반례 확인, 근거 기반 판단을 구분합니다.
- **다각도 분석:** 도구 개수가 아니라 의미·타입·흐름·구조·자원 수명 등 분석 관점의 깊이와 상호 보완성을 추구합니다.
- **보고서:** 발견 내용, 발생 조건, 영향, 코드 근거, 수정 방향, 검증 방법, 분석 공백을 연결합니다.

## 운영 원칙

개인 PC에서 시작하고 여러 노드의 워커로 확장합니다. **Native 실행이 기본이며 Docker·Podman은 선택**입니다. 최소·최대 워커 설정, 자동 증감, 자원 제한, 예약, 진행 상황은 초기 설계에 포함합니다.

GitHub.com·GHES의 저장소와 브랜치 또는 접근 가능한 로컬/NFS 경로를 등록합니다. 분석 시 소스를 별도 공간에 고정합니다. README·AGENTS·설계 문서는 자동으로 읽고, 문서가 없더라도 소스 기반으로 분석합니다. 사용자 설명·첨부는 선택입니다.

**기존 CI 연결·결과 수집·CI 워크플로 생성은 제품 범위 밖입니다. 의존성 CVE 검사는 초기 범위에서 제외합니다.** 의존성 선언은 실행 환경 파악에 계속 사용합니다. SSH 원격 작업 기능은 만들지 않습니다.

## 개발 시작

Go 1.27 이상, Node.js 22.12 이상을 준비하고 저장소 루트에서 실행합니다. 현재 검증한 조합과 DB 시험 방법은 [개발 가이드](docs/21-w01-foundation.md)에 있습니다.

```bash
go build -o bin/codeye ./cmd/codeye
./bin/codeye doctor --format json > doctor.json
npm --prefix web ci
npm --prefix web run dev
```

웹에서 `doctor.json`을 열면 실제 진단 결과를 로컬에서 확인할 수 있습니다. doctor 종료 코드 1은 실패·미설정·미검증 항목이 있다는 뜻이며 W01에서는 모델·프로세스 제어가 검증 전이므로 예상된 결과입니다. Docker는 필요하지 않습니다. Vite 단독 실행은 진단 뷰어로 사용할 수 있습니다. 인증 API와 연결하려면 웹을 빌드하고 W03 가이드에 따라 `codeye serve`로 여세요.

```bash
go test ./...
npm --prefix web test
npm --prefix web run build
```

## 문서 시작점

| 목적 | 문서 |
|---|---|
| 전체 문서 탐색 | [문서 인덱스](docs/INDEX.md) |
| 개발 에이전트의 첫 작업 | [AGENTS.md](AGENTS.md) |
| 제품 범위와 기능 | [제품 정의](docs/00-product-and-scope.md), [요구사항](docs/01-requirements.md) |
| 분석 품질의 기준 | [품질 원칙](docs/02-analysis-quality.md), [LLM·지침 설계](docs/07-llm-and-instructions.md) |
| 구조와 실행 | [아키텍처](docs/03-architecture.md), [Native 실행](docs/08-native-execution.md), [워커·자동 증감](docs/09-workers-and-scheduling.md) |
| 데이터와 화면 | [데이터 모델](docs/10-data-model.md), [API](docs/11-api-and-events.md), [보고서·UI](docs/12-report-and-ui.md) |
| 구현 착수 | [작업 계획](docs/17-implementation-plan.md), [수용 테스트](docs/18-acceptance-and-traceability.md) |
| 실제 분석 지침 초안 | [프롬프트 팩](prompts/review-v1/00-core.md) |
| 결과물의 구체적인 모습 | [실제 Go vet 보고서 JSON](examples/report-go-vet.json), [합성 예시 보고서](examples/report.md) |

## 기술 기본안

웹은 React·TypeScript 정적 배포, 관리 서버·노드 에이전트·워커는 Go, 상태와 작업 큐는 PostgreSQL을 사용합니다. 분석 도구와 대상 언어 런타임은 별도로 관리합니다. 사내 모델 연결은 허용된 OpenCode 경로를 우선하며, 직접 API는 허용된 환경의 선택 어댑터입니다. 세부 선택과 미확정 조건은 [결정 기록](docs/19-decisions-and-open-questions.md)을 따릅니다.

## 문서 묶음 검증

개발용 Python 환경에 PyYAML과 jsonschema를 준비한 뒤 실행합니다. 아래 검사는 CODEYE 제품의 동작 시험이 아니라 계약·예제·문서 연결의 검증입니다.

```bash
python scripts/validate_bundle.py
python -m unittest discover -s fixtures/cleanup -p 'test_*.py' -v
```

## 공개 범위와 라이선스

실제 사내 주소·경로·소스·자격증명을 게시하지 않습니다. 예시는 가상의 프로젝트와 설정입니다. 프로젝트 라이선스는 소유자가 결정하며 이 문서 묶음은 임의로 라이선스를 선언하지 않습니다. 분석 도구 실행·규칙팩 사용·바이너리 재배포 허용 여부는 각각 확인합니다.
