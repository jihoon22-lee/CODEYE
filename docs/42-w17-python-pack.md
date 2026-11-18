# 42. W17 Python 분석 팩

W17은 [분석기 팩 계약](39-r1-analyzer-packs.md)을 따르는 Python 팩이다. W19의 계약(Descriptor·Composite·envelope·공유 toolchain·저장소 코드 허가)은 바꾸지 않았다. 구현은 `internal/worker/analysis/python`, 연결은 `internal/worker/analysis/builtin/python.go`, 결정은 [A25](19-decisions-and-open-questions.md), 수용 시험은 [TC-108~110](18-acceptance-and-traceability.md)이다. 시험 프로젝트는 `fixtures/w17-python/`에 있다.

## 1. 구성 요약

| 분석기 | 역할 | 필수 | 고정 버전 | 실행 | 저장소 설정 | 소스 안 억제 |
|---|---|---|---|---|---|---|
| `python.ruff` | 기준. 오류 흐름·정의·예외 처리 | 예 | 0.16.x (0.16.3 측정) | `--ruff` 실행 파일 | `overridden` (`--isolated`) | `reported` (`--ignore-noqa` 두 번째 실행) |
| `python.mypy` | 타입 검사(pyright와 대안) | 아니요 | 2.4.x (2.4.1 측정) | `--mypy` 스크립트(자기 venv의 절대 `#!`) | `overridden` (`--config-file=`) | `honored` |
| `python.pyright` | 타입 검사(mypy와 대안) | 아니요 | 1.1.414 정확히 | `--node` + `--pyright` JS 진입 스크립트 | `overridden` (scratch `pyrightconfig.json`) | `honored` |
| `python.bandit` | 보안 | 아니요 | 1.9.x (1.9.4 측정) | `--bandit` 스크립트 | `overridden` (scratch `--ini`) | `reported` (`--ignore-nosec` 두 번째 실행) |

catalog 순서는 Go 뒤에 ruff, mypy, pyright, bandit이다. 모듈마다 도구별 task 하나를 계획한다. key는 루트 모듈이 `<도구>`, 나머지는 디렉터리 순으로 `<도구>.mNN`이다. 모듈 디렉터리는 `pyproject.toml`·`setup.cfg`·`.python-version`·`requirements*.txt`가 있는 디렉터리다. 숨김 디렉터리·숨김 파일·`__pycache__`·`__pypackages__`·`node_modules`·`site-packages`·`testdata`·`vendor`·`venv`는 분석하지 않는다. 스냅샷은 `pyvenv.cfg`가 있는 가상환경과 symlink를 이미 복사하지 않는다(W04). 범위에 Python 소스가 있지만 분석할 파일이 없으면 자리 task 하나가 `no_analyzable_python_files`로 미실행 envelope를 남긴다. W19의 `pack-gap.python` 공백은 이 팩으로 대체됐다. 그 결과 `--go`만 있는 노드에서 Python 스냅샷은 필수 ruff task가 `unsupported`(`tool_not_configured`)가 되어 보고서가 `partial`(`required_task_not_succeeded`)이다.

모든 task는 `RepositoryCode=false`다. 어떤 도구도 프로젝트의 interpreter·venv·plugin·설정 프로그램을 실행하지 않는다(§3).

## 2. 기준 분석기: ruff

[§2.6](39-r1-analyzer-packs.md#26-필수required-규칙)에 따라 생태계마다 기준 분석기 하나만 필수다. ruff를 고른 근거는 다음과 같다.

- **설치 의존이 가장 적다.** 단일 정적 바이너리라 Python interpreter도 node도 필요 없다. 그래서 ruff만 있는 노드도 deep-review를 광고할 수 있다.
- **저장소 코드를 실행하지 않는다.** `--isolated`이면 어떤 설정 파일도 읽지 않고 import 대상도 실행하지 않는다.
- **의존성 설치 없이 결과가 안정적이다.** 타입 검사기는 설치되지 않은 의존성 때문에 범위가 줄지만(§10), ruff의 선택 규칙(pyflakes·bugbear·오류 흐름)은 파일 단위라 영향이 없다.
- **빠르다.** fixture 기준 실행 하나가 수십 ms다.

mypy·pyright·bandit은 추가 깊이다. 실패하거나 미지원이어도 보고서에 보이지만 판정 필수 task 실패로 세지 않는다.

## 3. 저장소 코드와 저장소 설정

| 도구 | 저장소가 정할 수 있는 것 | CODEYE 처리 | 측정 |
|---|---|---|---|
| ruff | `ruff.toml`·`.ruff.toml`·`[tool.ruff]`(`extend`, `src`, `cache-dir`, 규칙) | `--isolated`로 모든 설정 파일을 읽지 않는다. 캐시는 `--no-cache`와 `RUFF_CACHE_DIR`(task scratch) | 저장소 `ruff.toml`이 E501만 고르게 해도 F401이 보고됨. 외부 `extend` 파일의 canary가 stream에 없음 |
| mypy | `mypy.ini`·`.mypy.ini`·`setup.cfg`·`[tool.mypy]`(`plugins`, `mypy_path`, `files`, `cache_dir`, 보고서) | `--config-file=`(빈 값)으로 설정 파일을 읽지 않는다. 경로 형식 `plugins`는 Python 코드 실행이므로 저장소 설정을 따르는 방식은 두지 않았다. 캐시·보고서는 scratch | 설정 없이 실행하면 경로 plugin이 import되어 표지 파일과 `__pycache__`를 쓴다. `--config-file=`이면 쓰지 않고 `disable_error_code`도 적용되지 않음 |
| pyright | `pyrightconfig.json`·`[tool.pyright]`(`extraPaths`, `stubPath`, `typeshedPath`, `venvPath`/`venv`, `include`/`exclude`, 규칙 수준) | `-p <scratch>/cfg`의 CODEYE `pyrightconfig.json`만 읽는다. 실행 root는 모듈 디렉터리의 상대 경로다. 빈 scratch venv(`venvs/codeye-empty/lib/python3.N/site-packages`)를 지정한다 | 저장소 설정의 `typeCheckingMode = "off"`가 적용되지 않음. 설정이 없으면 pyright가 PATH의 `python`·`python3`을 실행해 검색 경로를 묻는다. CODEYE 설정에서는 호출 기록이 없음(가짜 interpreter) |
| bandit | `.bandit`(대상이 `.`일 때 자동으로 읽음), `-c` YAML·pyproject | `--ini <scratch>/cfg/bandit.ini`(빈 `[bandit]`)가 `.bandit`을 대신한다. `-c`는 쓰지 않는다 | 저장소 `.bandit`의 `skips = B602,B404`가 적용되지 않음 |

네 도구 모두 저장소 설정을 통째로 CODEYE 관리 설정으로 대신하므로 `rule_pack.repository_config=overridden`, `config_files=[]`다. 저장소 설정 값을 읽어 쓰는 경로가 없으므로 "작업 공간 밖을 가리키는 설정 값을 거부"할 대상도 없다(§9).

Python 스크립트 도구(mypy·bandit)는 운영자가 설치한 자기 venv의 절대 `#!` interpreter로 실행한다. `#!/usr/bin/env`는 PATH 검색이라 거부한다. 환경에는 `PYTHONDONTWRITEBYTECODE=1`·`PYTHONNOUSERSITE=1`·`PYTHONSAFEPATH=1`을 둔다. mypy는 `--no-site-packages`라 어떤 interpreter에도 설치 패키지를 묻지 않는다. pyright는 공유 node toolchain이 진입 스크립트를 실행하고 PATH는 node 디렉터리 하나다([§2.4.1](39-r1-analyzer-packs.md#241-공유-interpreter-toolchain)).

## 4. 대상 Python 버전

버전은 env.discover와 같은 규칙으로 모듈 선언에서 읽는다. 순서는 `pyproject.toml` `[project] requires-python`, `setup.cfg` `[options] python_requires`, `.python-version` 첫 버전이다. 스냅샷 파일을 manifest SHA-256으로 확인한 뒤 읽는다. 분석기 팩은 import 그래프에서 discovery 아래에 있다(discovery 시험이 builtin catalog를 만든다). 그래서 팩은 discovery를 import하지 않고 세 reader를 규칙 그대로 옮겨 쓴다. `TestDeclarationMatchesDiscovery`가 생성한 5천여 표본에서 `discovery.PythonRequirement`와 같은 결과(kind·constraint·malformed 판정)를 고정한다.

- PEP 440 지정자 목록에서 하한(`>=`, `~=`, `==`, `>`)이 가장 높은 것의 3.x minor를 쓴다. `.python-version`은 `3.N[.M]`만 이해한다.
- 이해하지 못한 선언은 `python_version_constraint_not_understood`, 읽지 못한 선언은 `python_version_declaration_unreadable`로 남기고 다음 선언을 본다.
- 선언이 없으면 3.14를 쓰고 `python_version_source=codeye_default`와 `python_version_unknown`을 남긴다. 3.14는 고정한 모든 도구가 받는 가장 새 버전이다. 오래된 기본값은 새 문법을 구문 오류로 만들지만 새 기본값은 버전별 진단 일부를 놓칠 뿐이라서 이렇게 정했다.
- 도구마다 받는 범위로 맞추고 벗어나면 `python_version_below_tool_minimum`·`python_version_above_tool_maximum`을 남긴다. ruff 3.7~3.15, mypy 3.10~3.15(3.8은 exit 2로 거부), pyright 3.3~3.15다.
- envelope `target.properties`에 `python_version`(전달한 값), `python_version_source`, `python_version_declared`를 남긴다. ruff는 `target_version`(`py3N`)도 남긴다.
- bandit은 자기 interpreter의 ast로 파싱하므로 버전을 전달할 수 없다. `python_version_applied=false`와 `parser_python_version`을 남긴다.
- 한 모듈에 대상 버전 하나만 검사한다(`single_target_python_version`, 타입 검사기).

## 5. 타입 검사기 선택: mypy 또는 pyright

두 검사기는 같은 종류의 진단을 다른 규칙으로 낸다. 둘 다 실행하는 것은 운영자의 명시 선택이다.

- `--mypy`만 주면 mypy, `--pyright`(+ `--node`)만 주면 pyright가 실행된다. 다른 쪽 task는 계획되고 `unsupported`(`tool_not_configured`)로 끝난다. 그래서 하나만 설정한 노드의 `static_analysis` 단계는 `partial`이다(A22 (3)과 같은 이유).
- 두 경로를 함께 주려면 `--python-both-type-checkers`가 필요하다. 없으면 agent가 사용법 오류(exit 2)로 시작하지 않는다. 경로가 둘 다 없는데 이 플래그만 있어도 사용법 오류다.
- 둘 다 선택하면 두 task에 `python_type_checkers_both_selected`를 남긴다. 이것은 선택 기록이다. 다른 쪽이 실제로 실행됐는지는 그 task의 상태가 기록한다(예: `--node`가 없으면 pyright는 `toolchain_not_configured`).
- 무엇이 실행됐는지는 task·envelope의 `analyzer.id`와 상태로 보고서 도구 행에 남는다. 두 검사기의 같은 구성 진단은 별도 관찰이다. 같은 분석기·규칙·위치·메시지만 한 finding이 된다(TC-102).

이 선택은 agent 플래그 검사로만 집행한다. hello `analyzer_tools`에 새 필드를 두지 않았다(프로토콜 변경 없음). 워커는 두 경로가 모두 오면 둘 다 실행한다.

## 6. 규칙 팩

| 규칙 팩 | 선택(`rule_pack.selection`) |
|---|---|
| `codeye-python-ruff` v1 | `--isolated --no-respect-gitignore --select E711,E712,E713,E714,E721,E722,E9,F,B,BLE,PLE,ASYNC --ignore B905,B911,B912 --unfixable ALL`. 비교·bare except·IO/구문(E9), pyflakes, bugbear, blind except, pylint 오류, async. 스타일 규칙과 S(flake8-bandit)는 고르지 않는다. B905·B911·B912는 `strict=` 명시 요구라 제외 |
| `codeye-python-mypy` v1 | `--config-file= --no-site-packages --explicit-package-bases --follow-imports=silent --check-untyped-defs --no-native-parser --disable-error-code=await-not-async,name-defined,no-redef,str-format`(코드마다 한 플래그). `--no-native-parser`는 비 ASCII 줄에서 native parser의 열이 틀려서(측정) |
| `codeye-python-pyright` v1 | `config=codeye-managed typeCheckingMode=standard` + 중복 규칙 5개 `none`(§7) |
| `codeye-python-bandit` v1 | `--ini=codeye-managed --skip B101,B105,B106,B107,B613`. B101 assert는 pytest assert마다 나오고, B105~B107 하드코딩 비밀번호는 메시지에 값 자체가 들어가므로(비밀정보는 W21 팩이 redaction과 함께 다룬다), B613은 ruff PLE2502와 같은 구성이라 뺐다 |

모든 도구의 stdout 상한은 32 MiB, stderr 4 MiB, 제한시간 15분이다. 잘림·부분 parse·timeout·취소·정리 불확실은 성공이 아니다.

## 7. 엔진 중복과 제외

같은 줄·같은 구성을 두 엔진이 보고하는지 `fixtures/w17-python/overlap`에서 측정해 한쪽만 남겼다. ruff가 기준이므로 타입 검사기 쪽을 껐다.

| ruff 규칙 | mypy | pyright | 처리 |
|---|---|---|---|
| F821 정의되지 않은 이름 | `name-defined` | `reportUndefinedVariable` | 두 검사기에서 끔 |
| F811 재정의 | `no-redef` | `reportRedeclaration`(첫 선언 줄) | 두 검사기에서 끔 |
| F507/F524 `%`·`format` 인자 | `str-format` | 없음 | mypy에서 끔 |
| PLE1142 async 밖 `await` | `await-not-async` | 규칙 없는 구문 오류 | mypy에서 끔. pyright는 CPython처럼 구문 오류로 보므로 그대로 둔다 |
| F631 tuple assert | `misc`("Assertion is always true") | `reportAssertAlwaysTrue` | pyright에서 끔. mypy `misc`는 다른 검사도 담아 끌 수 없어 남긴다 |
| B015 쓸모없는 비교 | 없음 | `reportUnusedExpression` | pyright에서 끔 |
| PLE0604 `__all__`의 비문자열 | `list-item`(다른 검사) | `reportUnsupportedDunderAll` | pyright에서 끔 |
| PLE2502 양방향 제어 문자 | 없음 | 없음 | bandit B613을 끔 |

ruff의 S 규칙은 bandit 검사를 다시 구현한 것이다. bandit이 보안 엔진이므로 S는 고르지 않았다. E722/BLE001과 bandit B110/B112는 같은 줄에 나오지만 다른 검사(예외 범위 대 예외 무시)라 둘 다 둔다.

## 8. 소스 안 억제

| 계열 | 측정 | 처리 |
|---|---|---|
| ruff `# noqa: X`, bare `# noqa`, `# ruff: noqa: X`, `# flake8: noqa`, `# ruff: ignore[X]`, `# ruff: disable[X]`/`enable` | `--ignore-noqa`가 여섯 형식을 모두 끈다 | `reported`. 억제 존중 실행과 `--ignore-noqa` 실행의 차이가 `suppressed=true` 관찰이다. 원시 참조는 `--ignore-noqa` stdout이다 |
| bandit `# nosec`, `# nosec B602`, import 줄 nosec | `--ignore-nosec`가 모두 보인다. `# nosec B101`은 B602를 숨기지 않는다 | `reported`. 같은 두 실행 차이 |
| mypy `# type: ignore[...]`, `# mypy: ignore-errors` | 숨긴 오류를 보고할 방법이 없다 | `honored`, `unreported_suppression_rules=[]` |
| pyright `# type: ignore`, `# pyright: ignore[rule]` | 숨긴 진단을 보고할 방법이 없다. 틀린 규칙 이름은 숨기지 않는다 | `honored`, `unreported_suppression_rules=[]` |

두 실행 차이는 (파일·규칙·위치·메시지) 다중집합으로 맞춘다. 억제 존중 실행에만 있는 진단이 있으면 `suppression_diff_inconsistent`로 실패한다. 계약([§5.1](39-r1-analyzer-packs.md#51-규칙-문맥과-소스-안-억제-builder-4-tc-104))상 `unreported_suppression_rules`는 `reported`일 때만 쓸 수 있으므로 `honored` 분석기는 빈 목록이다. 그 사라진 finding은 비교에서 해결로 보지 않는다(`source_suppressions_hidden`).

## 9. 보안

대상 저장소의 설정과 소스는 적대적 입력으로 본다.

1. **저장소 설정은 경로를 정하지 못한다.** 네 도구 모두 CODEYE 관리 설정으로 저장소 설정을 통째로 대신한다(§3). 그래서 `mypy_path`·`cache_dir`·`files`·`plugins`·`html_report`, pyright `extraPaths`·`stubPath`·`typeshedPath`·`venvPath`/`venv`·`include`, ruff `extend`·`src`·`cache-dir`, `.bandit`의 `targets`·`output` 같은 값은 읽히지 않는다. 쓰기 위치(캐시, 보고서, bandit JSON, pyright 설정·빈 venv)는 모두 task scratch다. `TestRealOutsideConfigCanary`(도구별)와 `run.TestPythonPackOutsideConfigCanary`(production 구성)는 작업 공간 밖 임시 디렉터리에 고유 canary를 두고 위 옵션 전부로 가리킨다. canary는 어떤 stream·envelope·업로드 artifact·보고서에도 없고 밖 디렉터리에는 아무것도 쓰이지 않는다. 대조 실행(같은 프로젝트에서 CODEYE 플래그 없이)에서는 mypy가 외부 plugin을 실행하고 외부 캐시를 썼으며 ruff는 외부 `extend` 파일의 canary를 출력했다.
2. **작업 공간 밖 파일의 진단은 버린다.** 도구가 보고한 파일이 작업 공간 사본 밖으로 풀리면(절대 경로, `..`) 그 관찰과 tool error를 메시지째 버리고 개수만 `diagnostics_outside_workspace_dropped` 제약으로 남긴다. 처리 파일로도 세지 않는다. 원시 stdout artifact에는 도구가 낸 그대로 남는다(executor의 경로 redaction은 적용된다).
3. **수집 stream에 소스 줄을 두지 않는다.** ruff는 fix edit에 원본 소스를 옮겨 적으므로(`"content": "value is None"`, 여러 줄 삽입) `--unfixable ALL`로 `fix`를 비운다. bandit JSON은 `-n 0`이어도 `code`에 소스 줄을 담으므로 scratch 파일(`-o`)로만 쓰고, stream 관찰은 소스 줄이 없는 CSV에서 읽는다. mypy `-O json`과 pyright `--outputjson`에는 소스 줄이 없다. `TestRealStreamsHoldNoSourceLines`가 네 도구의 모든 stream에서 fixture 소스 줄(12자 이상)이 없음을 확인한다.
4. **저장소에서 온 위치 인자는 옵션으로 읽히지 않는다.** ruff·bandit 대상은 `--` 뒤에 두고 `.`이 아니면 `./`를 붙인다. mypy `@` 파일 목록의 각 줄(인자로 읽힌다)과 pyright stdin 목록도 `./`로 시작한다. W04 경로 검사가 제어 문자를 막으므로 줄바꿈으로 목록을 나눌 수 없다. 하위 모듈 디렉터리 이름에 쉼표나 glob 문자가 있으면 ruff·bandit 제외 목록이 나뉘거나 확장되므로 그 task는 `nested_module_name_unsupported`로 실행을 거부한다. `TestRealOptionLikeFileNames`는 `--help.py`·`-x.py`·`pkg/-y.py`를 전체 범위와 경로 범위에서 네 도구로 분석해 모두 파일로 처리됨을 확인한다.

## 10. import·stub 공백과 coverage 단위

의존성을 설치하지 않는다. 해결되지 않은 import는 결함이 아니라 분석 범위 공백이다.

| 도구 | 진단 | coverage 단위 | 제약 |
|---|---|---|---|
| mypy | `import-not-found` | `import_unresolved` | `imports_unresolved` |
| mypy | `import-untyped`("Library stubs not installed") | `stub_missing` | `type_stubs_missing` |
| pyright | `reportMissingImports` | `import_unresolved` | `imports_unresolved` |
| pyright | `reportMissingModuleSource`(번들 stub은 있고 소스가 없음) | `import_source_missing` | `import_sources_missing` |
| pyright | `reportMissingTypeStubs` | `stub_missing` | `type_stubs_missing` |

측정 결과 두 검사기 모두 해결되지 않은 모듈을 `Any`로 다뤄 후속 오류(상속·속성·호출)를 내지 않는다(`fixtures/w17-python/imports`). 그래서 후속 오류를 따로 거르지 않는다. 타입 검사기 task에는 언제나 `third_party_packages_not_installed`가 붙는다. 처리 파일·`analyzed_*` 단위는 파일, import 단위는 진단 수다.

## 11. 결과 판정

| 도구 | exit | 판정 |
|---|---|---|
| ruff | 0 + 레코드 없음 / 1 + 레코드 | 성공(처리 파일은 `ruff.files`의 `--show-files` 목록) |
| ruff | 0·1이 레코드와 어긋남 / 2 | `exit_status_mismatch` / `ruff_failed` |
| mypy | 0 + 오류 없음 / 1 + 오류(blocking·구문 없음) | 성공(처리 파일은 `--lineprecision-report`의 모듈과 계획 파일 대조) |
| mypy | 2 + 구문 오류 / 2 그 밖 | `source_syntax_errors` / `mypy_failed` |
| pyright | 0·1이 summary 오류 수와 맞음 | 성공(`filesAnalyzed`가 계획 파일 수와 같을 때만 처리 파일 인정) |
| pyright | 2·3·4 | `pyright_failed` |
| bandit | 두 실행 모두 0 + 결과 없음 / 1 + 결과 | 성공(처리 파일은 scratch JSON `metrics`에서 `errors` 파일을 뺀 것, `.py`만) |
| bandit | 결과와 어긋남 / 2 | `exit_status_mismatch` / `bandit_failed` |

공통 규칙은 다음과 같다.

- 구문 오류(ruff `invalid-syntax`, mypy `syntax`, pyright 규칙 없는 오류, bandit `errors`의 구문 오류)는 관찰이 아니라 그 파일의 tool error다. `syntax_error` 단위로 세고 task는 `source_syntax_errors`로 실패한다. 성공 envelope에는 tool error가 없어야 하기 때문이다(W19와 같음).
- 계획했지만 도구가 처리 목록에 넣지 않은 파일은 처리 파일로 세지 않는다. `files_unlisted`·`not_analyzed`와 `planned_files_not_processed`로 남긴다.
- 알아보지 못한 버전 출력이면 도구를 코드에 실행하지 않는다(`tool_version_unrecognized`). 시험하지 않은 버전은 실행하고 `tool_version_untested`를 남긴다.
- 위치는 snapshot 바이트로 바이트 열(1부터)로 바꾼다. ruff는 code point 열, pyright는 0부터의 UTF-16 열, mypy(`--no-native-parser`)와 bandit은 UTF-8 바이트 열이다(é·😀로 측정).
- 진단은 검토 전 낮은 심각도의 도구 관찰이다. bandit `issue_severity`는 `raw_severity`로만 남는다.

## 12. 시험과 실행 근거

캡처(`internal/worker/analysis/python/testdata/`)는 다음 명령으로 만들었다. 모듈 디렉터리는 `@MODULE@`, 홈 디렉터리는 `@HOME@`로 저장한다. bandit CSV는 CRLF로 끝나므로 그 디렉터리의 `.gitattributes`가 줄바꿈 변환을 끈다.

```text
CODEYE_TEST_RUFF=$HOME/.local/bin/ruff \
CODEYE_TEST_MYPY=$HOME/.local/share/codeye-tools/bin/mypy \
CODEYE_TEST_BANDIT=$HOME/.local/share/codeye-tools/bin/bandit \
CODEYE_TEST_PYRIGHT=$HOME/.local/share/codeye-tools/node/node_modules/pyright/index.js \
CODEYE_TEST_NODE=$HOME/.local/lib/node-v24.18.0-linux-x64/bin/node \
CODEYE_W17_CAPTURE=<dir> go test -count=1 -run 'TestReal' ./internal/worker/analysis/python/
```

[실제 envelope 예시](../examples/observations-ruff.json)는 `CODEYE_W17_DUMP=<dir>`로 얻은 `TestRealRuffFindings` 결과다. 도구 서술은 [ruff](../examples/analyzer-ruff.yaml)·[mypy](../examples/analyzer-mypy.yaml)·[pyright](../examples/analyzer-pyright.yaml)·[bandit](../examples/analyzer-bandit.yaml)에 있다.

| 시험 | 범위 |
|---|---|
| `python.TestReal*` (도구 필요, TC-108~110) | 도구마다 findings·정상·구문 오류·억제 계열·저장소 설정 무시·중복 제외·import 공백. ruff 하위 모듈·제외 디렉터리, mypy plugin 미실행, pyright가 interpreter를 실행하지 않음(가짜 `python`·`python3`), 원본·스냅샷 불변, 작업 공간에 캐시·bytecode 없음, 노드 경로 미노출 |
| `python.TestRealOutsideConfigCanary`, `TestRealStreamsHoldNoSourceLines`, `TestRealOptionLikeFileNames` (도구 필요) | §9의 1·3·4 |
| `python.TestReplay*` (CI) | 캡처 재생으로 위 판정. 네 도구의 timeout·취소·정리 불확실·잘림·parse 실패·exit 불일치, 버전 미인식·미시험, 억제 차이 불일치, 처리 파일 검증(ruff 목록 누락, pyright `filesAnalyzed`, mypy lineprecision 없음), 작업 공간 drift, 시작 전 취소, 작업 공간 밖 진단 버림 |
| `python.TestDeclarationMatchesDiscovery`, `TestResolveTarget`, `TestMinimumMinorAndClamp` | §4 |
| `python.TestPlanModules`, `TestPlanKeysAndScope`, `TestByteColumn`, `TestParseDocumentRanges`, `TestVersionParsers`, `TestRulePackSelections`, `TestArgvPositionalsNeverReadAsOptions` | 계획·key·필수·범위, 열 변환, JSON 범위, 버전, 규칙 팩, argv |
| `builtin.TestHandlers*`, `TestToolFlagsAndProbes` | catalog 순서, 도구 플래그, 미설정 Python 멤버, 탐색 probe |
| `main.TestPythonTypeCheckerChoice` | §5의 agent 플래그 규칙 |
| `run.TestPythonSnapshotWithoutRuff` (Linux, TC-108) | `--go`만 있는 노드의 Python 스냅샷: 정적 task 네 개, ruff 필수 `unsupported`, `pack-gap.python` 없음, 보고서 `partial`·`required_task_not_succeeded`, 탐색 `not_probed` |
| `run.TestPythonPackWithRealTools`, `run.TestPythonPackOutsideConfigCanary` (Linux, 도구 필요) | 실제 워커 production 구성에서 네 도구 성공·실행 기록·버전 출처, 외부 설정 canary가 artifact·보고서에 없음 |
| `run.TestNonGoSnapshotsShowTheGapNotARequiredFailure` (Linux, TC-69) | 공백 행을 JavaScript 스냅샷으로 옮김 |

## 13. 남은 제한

- Windows에서 실제 도구를 실행하지 않았다. Windows는 `GOOS=windows go vet`까지만 확인했다. bandit·mypy 스크립트의 `#!` 검사는 Unix에서만 한다.
- 모듈마다 대상 버전·플랫폼 하나만 검사한다. 조건부 import·버전 분기는 그 조합에서만 본다.
- 타입 검사는 설치되지 않은 의존성을 `Any`로 보므로 그 경계의 타입 오류는 보지 못한다(§10). 프로젝트 venv를 쓰는 방식은 저장소 코드 실행이라 두지 않았다.
- mypy `misc`(tuple assert)는 ruff F631과 같은 구성이지만 끌 수 없어 두 관찰이 남는다.
- ruff와 bandit의 처리 파일 검증은 도구 목록 기준이다. ruff 기본 제외(`build`, `dist` 등)에 걸린 계획 파일은 `not_analyzed`로 보인다.
- 노트북(`.ipynb`)은 분석하지 않는다(ruff 제외 목록, 계획에도 없음).
- `honored` 억제(mypy·pyright)의 사라진 finding은 비교에서 해결로 보지 않는다.
- 작업 공간 밖 진단의 원시 텍스트는 stdout artifact에 남는다(envelope·보고서에는 없음).
- 두 타입 검사기 선택은 agent 플래그 검사로만 집행한다. hello를 직접 만드는 다른 클라이언트는 없다.
