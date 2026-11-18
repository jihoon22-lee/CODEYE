# W18 JS/TS 분석 팩

W18은 [분석기 팩 계약](39-r1-analyzer-packs.md)(W19)을 따르는 JavaScript·TypeScript 팩이다. ESLint 기준선, tsc, type-aware ESLint 세 분석기를 공유 `--node` toolchain으로 실행한다. 요구사항은 REQ-007/008/012/013/015, 수용 시험은 TC-111~TC-113, 결정은 [A26](19-decisions-and-open-questions.md)이다. 구현은 `internal/worker/analysis/{jsts,eslint,tsc}`와 `builtin/javascript.go`이다.

이 문서의 수치는 Linux(WSL2)의 node v24.18.0, eslint 10.15.0, @eslint/js 10.0.1, typescript-eslint 8.73.0, typescript 6.0.3에서 잰 값이다. Windows는 `GOOS=windows go vet`만 확인했다.

## 1. 구성 요약

| 분석기 | 이름·task key | Required | 계획 단위 | 저장소 코드 실행 |
|---|---|---|---|---|
| `javascript.eslint` | `eslint`, `eslint.mNN` | 예(생태계 기준) | JS/TS 소스가 있는 모든 모듈 | 아니오 |
| `javascript.tsc` | `tsc`, `tsc.mNN` | 아니오(추가 깊이) | `tsconfig.json`이 있는 모듈 | 아니오 |
| `javascript.eslint_typed` | `eslint-typed`, `eslint-typed.mNN` | 아니오(추가 깊이) | `tsconfig.json`과 TS 소스가 있는 모듈 | 아니오 |

- 모듈은 W15 탐색과 같은 규칙으로 찾는다. `package.json`이 있는 디렉터리(testdata·숨김 디렉터리 제외)가 모듈이고, 어느 모듈에도 속하지 않는 소스는 루트 모듈(`.`)에 둔다. 루트에 `package.json`이 없으면 `no_package_json_module` 제약을 남긴다. 중첩 모듈의 파일은 그 모듈의 작업이 맡는다. 번호는 경로 순서이며 루트는 번호 없이 이름만 쓴다. `jsts.PackageModules`는 `discovery.DetectModules`와 같은 결과를 내도록 시험으로 묶었다(discovery 시험이 분석기 목록을 import하므로 import 순환을 피해 다시 썼다).
- 각 모듈의 tsconfig 선택은 `<모듈>/tsconfig.json` 하나다. tsc와 type-aware 작업은 그 체인(`extends`·`references`)을 `rule_pack.config_files`에 경로와 SHA-256으로 기록한다.
- JS 전용 모듈은 기준선만 받는다. `checkJs`는 켜지 않는다. 저장소가 `allowJs`·`checkJs`를 켠 tsconfig는 tsc가 그대로 따른다.
- 세 분석기 모두 운영자가 `--eslint`, `--tsc`로 지정한 JS 진입 스크립트(`eslint/bin/eslint.js`, `typescript/bin/tsc`)를 `<node> <script> <args>`로 실행한다. `#!` 줄과 PATH 탐색을 쓰지 않는다. 진입 스크립트는 절대경로이고, 같은 이름의 npm 패키지 안에 있어야 하며, shell·`.cmd` shim은 거부한다. `--node` 없이 도구만 지정하면 `toolchain_not_configured`다.

### 1.1 기준 분석기 선택(§2.6)

기준선은 ESLint의 구문 규칙(`@eslint/js` recommended, TS 파일에는 typescript-eslint recommended)이다. tsc는 기준선이 될 수 없다. 스냅샷은 `node_modules`를 항상 제외하므로 실제 저장소에서 tsc는 대부분 의존성 공백으로 끝난다(§4의 실측). 기준선이 공백으로 실패하면 거의 모든 JS/TS run의 판정이 공백이 된다. 구문 규칙은 타입 정보 없이 JS 전용 모듈과 TS 모듈 모두에서 끝까지 실행된다. tsc와 type-aware 규칙은 의존성이 갖춰진 경우의 추가 깊이다.

## 2. 실행

모든 실행은 executor Runner에 argv·cwd(작업 사본의 모듈 디렉터리)·환경(`PATH`=node 디렉터리, `LANG=C.UTF-8`, Windows는 `SystemRoot`)·제한시간(15분)·출력 상한을 준다. 스냅샷 디렉터리는 보호 경로다. 임시 디렉터리와 설정 파일은 task scratch(`analyzers/<key>/`)에 둔다.

```text
eslint: <node> <eslint.js> --no-config-lookup -c <scratch>/cfg/eslint.config.cjs --no-inline-config
        --suppressions-location <scratch>/cfg/eslint-suppressions.json --no-warn-ignored
        --no-error-on-unmatched-pattern --format json -- <./경로...>
tsc:    <node> <tsc> --noEmit --pretty false --incremental false --composite false --ignoreDeprecations 6.0
        --noUnusedLocals false --noUnusedParameters false --noFallthroughCasesInSwitch false -p ./tsconfig.json [--showConfig]
```

- 실행 순서: eslint는 `eslint.version` → `eslint.check`. tsc는 `tsc.version` → `tsc.config`(`--showConfig`) → `tsc.check`. 버전을 알아보지 못하면 검사를 실행하지 않는다.
- **solution 설정.** `files: []`와 `references`만 있는 설정(Vite 구성)은 tsc가 아무것도 검사하지 않고 exit 0을 낸다(실측). 이때는 모듈 안의 참조 프로젝트를 최대 8개까지 하나씩 `tsc.config.rNN`·`tsc.check.rNN`으로 검사하고 `solution_config_references_checked`를 남긴다. 모듈 밖 참조와 상한 초과는 제약으로 남긴다.
- **결과 위치.** 관찰의 `raw_ref`는 기록된 stdout·stderr 흐름의 SHA-256과 크기에 묶여야 하고 보고서 decoder가 작업 산출물과 대조한다. 그래서 eslint JSON은 `-o` 파일이 아니라 stdout으로 받는다. 브리프의 `-o` 안과 다른 선택이다.
- **열 단위.** ESLint와 tsc의 열은 UTF-16 단위다. 스냅샷 원문(BOM 제외)으로 byte 열로 바꾸고, 바꾸지 못하면 `column_unit: utf16`으로 원값을 둔다.
- **캐시와 쓰기.** eslint는 `--cache`를 쓰지 않는다. tsc는 `--noEmit`에 `--incremental false --composite false`를 더한다. `--noEmit`만으로는 incremental·composite 설정이 `tsconfig.tsbuildinfo`를 쓴다(실측). `--build`는 쓰지 않는다. 작업 사본 트리 해시가 실행 전후 같음을 시험한다.
- **TypeScript 6 폐기 옵션.** `moduleResolution: node`, `baseUrl` 같은 6.0 폐기 옵션은 TS5101/TS5107로 옵션 단계에서 검사를 멈춘다. `--ignoreDeprecations 6.0`으로 검사를 계속하고, type-aware 작업에서 보이면 `typescript_deprecations_ignored`를 남긴다.

## 3. 저장소 코드와 비신뢰 설정

### 3.1 작업별 저장소 코드 분류

세 작업 모두 `RepositoryCode=false`다. 근거는 다음과 같다.

- **eslint 기준선·type-aware.** 저장소의 `eslint.config.*`·`.eslintrc.*`는 실행 가능한 JS다. `--no-config-lookup -c <관리 설정>`이면 읽지 않는다. 실행되면 표식 파일을 남기는 `eslint.config.js`를 둔 fixture에서 표식이 생기지 않았다(`TestRealESLintMonorepo`). 관리 설정은 task scratch에서 `@eslint/js`·`typescript-eslint`를 eslint와 같은 도구 node_modules의 절대 진입 경로로 `require`한다. 저장소 `node_modules`에서 plugin·parser를 찾지 않는다. 저장소 설정을 따르는 실행(`RepositoryCode=true`)은 구현하지 않았다. 그래서 `repository_config: overridden`과 `repository_eslint_config_not_loaded`를 기록한다.
- **tsc.** `tsconfig.json`은 JSONC 데이터로 읽는다. tsc는 tsconfig의 언어 서비스 `plugins`를 읽지 않는다. 실행되면 표식을 남기는 plugin을 `node_modules`에 두고 tsc를 직접 실행해 확인했다(`TestRealTscDoesNotLoadTSConfigPlugins`). `_tsc.js`의 `sys.require`는 호출하는 곳이 없다. source-map-support는 `NODE_ENV=development`일 때만 읽고, 이 환경 변수는 넘기지 않는다.
- **type-aware.** typescript-eslint의 `parserOptions.project` 프로그램도 tsconfig plugin을 읽지 않는다(같은 표식 시험). 관리 설정의 `project`는 `./tsconfig.json`이고 `tsconfigRootDir`는 모듈 디렉터리다. 절대 작업 경로에 `(`·`[`·`*`가 있으면 typescript-eslint 8.73이 그 경로를 glob으로 확장한다. 그래서 상대 경로를 쓴다(실측: 공백·`&`·`()`가 있는 경로에서 절대 경로 설정은 실패했다).
- **의존성 코드.** 모듈 경로나 그 상위에 `node_modules`가 있으면 tsc와 type-aware 작업은 실행 전에 `skipped_policy`(`node_modules_outside_snapshot`)로 끝난다. 스냅샷에는 `node_modules`가 없으므로 그런 디렉터리는 출처를 manifest에 묶을 수 없다. 기준선은 모듈 해석을 하지 않으므로 계속 실행한다.

### 3.2 tsconfig는 비신뢰 입력이다

tsc와 type-aware 작업은 도구를 실행하기 전에 tsconfig 체인 전체를 검사한다. 체인은 루트 설정, 로컬 `extends`, `references`다. 기록에는 옵션 이름만 남긴다(`tsconfig_option_<snake_name>` 제약). 값은 남기지 않는다.

- **쓰기 옵션 → `skipped_policy`(`tsconfig_write_option`).**
  - `generateTrace`는 `--noEmit`에서도 trace.json·types.json을 쓴다(실측). `generateCpuProfile`은 명령행에서 끌 값이 없다. 두 옵션은 값과 무관하게 거부한다.
  - `outDir`·`declarationDir`·`outFile`·`tsBuildInfoFile`은 적용 플래그 아래에서 아무것도 쓰지 않는다. 바깥 canary 디렉터리를 가리켜도 비어 있었다(`TestRealTscWriteOptionsCanary`). 그래서 스냅샷 안을 가리키면 허용하고, 절대경로이거나 스냅샷을 벗어나면 거부한다.
  - 실효 설정(`--showConfig`)에서 `noEmit`이 true가 아니거나 `incremental`·`composite`가 켜져 있어도 검사 전에 거부한다.
- **호스트 경로 → `skipped_policy`(`tsconfig_outside_snapshot`).** 다음 값이 절대경로이거나 정규화 후 스냅샷을 벗어나면 거부한다. 대상은 `files`·`include`·`exclude`·`baseUrl`·`rootDir`·`rootDirs`·`typeRoots`, 경로 모양의 `types`, `paths`(자기 `baseUrl` 또는 설정 디렉터리 기준), `extends`·`references`다. `${configDir}`는 검사 대상 설정의 디렉터리로 푼다. 작업 사본 안에서 symlink를 따라 밖으로 나가는 경로도 거부한다(`tsconfig_option_symlink`). 스냅샷은 symlink를 복사하지 않는다(`link_not_followed`). 이 검사는 바뀐 작업 사본을 막는 방어다. `--showConfig` 결과도 같은 검사를 거친다. 루트 파일이 스냅샷 밖이면 검사 전에 `program_files_outside_snapshot`으로 끝난다.
- **진단 본문.** 도구 메시지의 작업·도구 경로는 표식으로 바꾼다. 남은 따옴표 안 절대경로는 `[host path withheld]`로 바꾼다. 작업 사본 밖 설정 파일의 진단은 본문 없이 기록한다.
- **argv.** 저장소에서 온 위치 인자는 option으로 해석되지 않게 한다. eslint는 `--` 뒤에 `./` 접두 경로를 둔다. `--`를 지원하는지는 실측했다. 접두 없는 `--help.ts`는 도움말을 출력했다. tsc는 `--`를 받지 않는다(TS5023). 그래서 `-p` 값에 항상 `./`를 붙인다. `--help.ts`·`-x.js` 파일 fixture로 확인했다(`TestRealESLintOptionLikeFileNames`, `TestRepositoryArgvNeverAnOption`).
- **ESLint 쪽 확인.** 기준선은 tsconfig를 읽지 않는다. 관리 설정의 `ignores`·`files`는 모듈 기준 상대 glob이고 cwd는 모듈 디렉터리다. `.eslintignore`는 eslint 10이 읽지 않는다. `ESLintIgnoreWarning`만 내며 제약 `eslintignore_unsupported`로 남긴다. `package.json`의 `eslintConfig`는 flat config가 읽지 않는다. cwd의 `eslint-suppressions.json`은 관리 경로가 대신한다. type-aware의 `parserOptions.project`는 관리 값으로 고정했다. 이 작업이 읽는 tsconfig 체인에는 tsc와 같은 검사(위)를 적용한다.

## 4. 의존성 공백

해석되지 않는 모듈·타입은 coverage 공백이다. 결함이나 clean으로 세지 않는다. 공백 코드는 `jsts.GapCodes`(TS2307, 2580, 2582, 2591, 2593, 2688, 2792, 2867, 2868, 2875, 2882, 6305, 7016, 7026)다.

CODEYE `web/src`를 node_modules 없이 복사해 tsc로 쟀다.

| 조건 | 결과 |
|---|---|
| `types: ["vite/client"]` 그대로 | 옵션 단계 TS2688 하나에서 멈춤 |
| `types` 항목 제거 | TS7026 813, TS7006 71, TS2307 46, TS2875 15, TS2322 6, TS7053 1, TS2882 1, TS2503 1 |
| 실제 의존성 연결(main checkout의 `web/node_modules` symlink) | TS2307(복사 범위 밖 예시 JSON·생성 validator)과 TS7006 9뿐. TS2322 6건은 모두 사라짐 |

공백이 있는 실행에서는 공백과 무관해 보이는 TS2322도 의존성 누락의 결과였다. 그래서 공백이 하나라도 있는 tsc 실행은 `failed`(`dependency_unavailable`)다. 공백은 `dependency` tool error로, 나머지 진단은 모두 `withheld` tool error로 남기고 관찰로 내지 않는다. type-aware lint도 같다. 의존성 없는 `web/src`에서 no-unsafe-call 1861, no-unsafe-member-access 1150 등이 나왔고, 메시지가 "error typed value"·"could not be resolved"로 공백을 가리켰다. no-misused-promises 8건은 의존성이 있을 때만 나왔다. type-aware 작업은 관리 규칙 `codeye/program-diagnostics`로 프로그램의 설정·전역 진단과 파일별 공백 코드를 함께 보고받는다. 공백이나 설정 오류가 있으면 모든 진단을 보류한다(`diagnostics_withheld_incomplete_types`). 이 규칙의 비용은 `web/src`에서 3.05초에서 3.25초로 약 7%였다. TS 파일이 tsconfig 프로그램 밖이면 typescript-eslint의 치명 메시지를 `outside_tsconfig_program`(건너뜀)으로 센다. 구문 오류는 tsc가 타입 검사 전에 멈추므로 `syntax_errors`로 실패하고 구문 진단만 tool error로 남긴다.

## 5. 규칙 문맥과 소스 안 억제(§5.1)

| 분석기 | rule pack | repository_config | config_files | source_suppressions | unreported_suppression_rules |
|---|---|---|---|---|---|
| eslint | `codeye-js-eslint` v1 | overridden | `[]` | disabled | `[]` |
| eslint-typed | `codeye-ts-eslint-typed` v1 | honored(tsconfig) | 체인(열거 가능할 때) | disabled | `[]` |
| tsc | `codeye-ts-tsc` v1 | honored | 체인과 참조 체인 | honored | `[]` |

`selection`에는 plugin 버전(예: `@eslint/js@10.0.1 recommended`)과 끈 플래그를 적는다. plugin을 올리면 규칙 문맥이 바뀐다. 시험한 계열 밖의 plugin·도구 버전은 실행하되 `plugin_version_untested`·`tool_version_untested`를 남긴다.

억제 계열별 실측(eslint 10.15.0):

- `eslint-disable`·`-line`·`-next-line`·블록. inline 설정이 켜지면 `suppressedMessages`(kind directive)에 남는다.
- `/* eslint rule: off */`. 기록 없이 진단을 지운다. `/* eslint rule: error */`는 선택 밖 규칙을 켠다. 모르는 규칙은 "Definition for rule ... not found"를 낸다. `/* eslint-env */`는 ruleId null 메시지를 낸다.
- `eslint-suppressions.json`. cwd에 있으면 자동으로 읽는다(kind file). 없는 `--suppressions-location` 파일은 exit 2다.
- `--no-inline-config`와 빈 관리 suppressions 파일이면 위 계열이 모두 꺼져 모든 진단이 나오고 억제는 0건이다(TC-113, `TestRealESLintSuppressionsDisabled`). 그래서 eslint는 `disabled`다. 그 실행에서 억제 메시지가 하나라도 나오면 `source_suppressions_not_disabled`로 실패한다.

tsc의 `@ts-ignore`·`@ts-expect-error`·`@ts-nocheck`는 숨긴 진단을 보고하지 않는다. 그래서 `honored`다(§5.1: `unreported_suppression_rules`는 `reported`에서만 쓴다). 기준선 TS 규칙의 `@typescript-eslint/ban-ts-comment`가 이 주석을 볼 수 있다. 설명이 붙은 `@ts-expect-error`는 보고하지 않음을 확인했다. `@ts-ignore` 보고는 재지 않았다.

## 6. 엔진 중복

`TestRealEngineOverlapExcluded`는 같은 구성을 TS와 JS로 둔 모듈에서 두 도구를 함께 실행해 확인했다.

- typescript-eslint recommended의 eslint-recommended는 tsc가 검사하는 core 규칙 22개를 TS 파일에서 끈다. 대상은 constructor-super, getter-return, no-array-constructor, no-class-assign, no-const-assign, no-dupe-args, no-dupe-class-members, no-dupe-keys, no-func-assign, no-import-assign, no-new-native-nonconstructor, no-new-symbol, no-obj-calls, no-redeclare, no-setter-return, no-this-before-super, no-undef, no-unreachable, no-unsafe-negation, no-unused-expressions, no-unused-vars, no-with다. TS 파일에서 no-undef·no-dupe-class-members는 나오지 않았고, tsc가 TS2304·TS2393으로 보고했다.
- tsc의 `noUnusedLocals`·`noUnusedParameters`·`noFallthroughCasesInSwitch`는 저장소 설정과 무관하게 끈다. 기준선의 `@typescript-eslint/no-unused-vars`·`no-fallthrough`가 같은 구성을 보고하기 때문이다. 저장소가 켜도 TS6133·TS7029는 나오지 않았다.
- type-aware 규칙(`recommendedTypeCheckedOnly` 23개)은 eslint-recommended를 빼서 기준선과 겹치지 않는다. 기준선 69개(TS)·63개(JS)와 교집합이 없다.
- JS 파일은 타입 검사가 없으므로 core 규칙을 유지한다. no-undef는 관리 설정이 전역을 선언하지 않으므로 JS에서도 끈다.

## 7. 종료 코드와 결과

| 도구 | 종료 코드(실측) | 판정 |
|---|---|---|
| eslint | 0 오류 없음, 1 오류 메시지(치명 구문 오류 포함), 2 crash·설정 오류·대상 없음 | exit 0인데 오류가 있거나 exit 1인데 오류가 없으면 `exit_status_mismatch`. 2와 "Oops! Something went wrong!"는 `eslint_failed` |
| tsc | 0 진단 없음, 2 진단 있음(`--noEmit`), 1 명령행 오류·설정 로드 실패 | 레코드 유무와 0/2가 어긋나면 `exit_status_mismatch`. 그 밖의 코드는 `tsc_failed` |

- 잘린 출력은 `output_truncated`, 해석 못한 줄·JSON은 `output_unparsed`다. 성공으로 세지 않는다.
- 파일 상태 단위는 다음과 같다. eslint는 `linted_clean`·`linted_with_findings`·`parse_error`·`outside_tsconfig_program`·`not_linted`·`unknown`이다. tsc는 `checked`·`not_verified`·`dependency_gap`·`outside_tsconfig_program`이다. tsc의 `files_in_scope`는 모듈의 JS/TS 파일 수이고, 프로그램 루트가 아닌 파일은 건너뜀으로 센다.
- 처리 파일은 성공한 작업에서만, 실제로 처리된 파일로만 채운다.
- 설정 오류(`tsconfig_invalid`), 패키지 extends 미해석(TS6053 → `tsconfig_extends_unresolved`), 입력 없음(TS18003 → `unsupported`/`no_input_files`)은 성공이 아니다.

## 8. 시험과 캡처

- CI에서 도는 시험은 다음과 같다. `jsts`(모듈 계획·W15 일치·열 변환·JSONC·체인·경로 검사), eslint·tsc 재생 시험(`testdata/<case>.<execution>.{stdout,stderr,exit}`와 `<case>.order`의 실측 바이트), 보안 시험(가짜 도구, 실행하면 실패하는 runner), builtin 시험.
- 실도구 시험은 `CODEYE_TEST_NODE`·`CODEYE_TEST_ESLINT`·`CODEYE_TEST_TSC`가 없으면 skip한다. 캡처는 다음 명령으로 다시 만든다.

```bash
export CODEYE_TEST_NODE=$HOME/.local/lib/node-v24.18.0-linux-x64/bin/node
export CODEYE_TEST_ESLINT=$HOME/.local/share/codeye-tools/node/node_modules/eslint/bin/eslint.js
export CODEYE_TEST_TSC=$HOME/.local/share/codeye-tools/node/node_modules/typescript/bin/tsc
CODEYE_W18_CAPTURE=$PWD/internal/worker/analysis/eslint/testdata go test -count=1 -run 'TestRealESLint|TestRealEngine' ./internal/worker/analysis/eslint/
CODEYE_W18_CAPTURE=$PWD/internal/worker/analysis/tsc/testdata go test -count=1 -run TestRealTsc ./internal/worker/analysis/tsc/
```

- 예시: `examples/analyzer-eslint.yaml`, `examples/analyzer-tsc.yaml`, `examples/observations-eslint.json`·`observations-tsc.json`이다. 두 JSON은 `fixtures/w18-js-ts/input` 실행에서 얻은 실제 envelope다. `scripts/validate_bundle.py`의 `check_w18_contracts`가 이 예시와 Go의 `tsc.Flags`를 대조한다.

| TC | 내용 | 시험 |
|---|---|---|
| TC-111 | 모노레포(루트·TS·JS 모듈), tsconfig 체인 기록, 저장소 설정 미실행, 원본·스냅샷·작업 사본 불변, 노드 경로 없음 | `TestRealESLintMonorepo`, `TestRealTscMonorepo`, `TestRealTscSolutionConfig`, 재생 |
| TC-112 | 의존성 공백은 coverage 공백이고 진단을 보류함 | `TestRealTscDependencyGap`, `TestRealESLintTypedDependencyGap`, 재생 |
| TC-113 | 억제 계열별 실측과 `disabled`·`honored` 기록 | `TestRealESLintSuppressionsDisabled`, `TestRealTscMonorepo`(@ts-expect-error) |

## 9. 남은 제한

- 저장소 ESLint 설정을 따르는 실행(저장소 코드 허가 필요)은 구현하지 않았다. 저장소가 정한 규칙·plugin 결과는 보이지 않는다.
- 의존성을 설치하지 않으므로 외부 패키지를 import하는 TS 모듈의 tsc·type-aware 결과는 대부분 공백(실패)이다. 기준선만 결과를 낸다.
- `prompts/review-r1/languages/`에는 go.md만 있다. JS/TS 비동기·경계 검토 지침은 이번 단계에서 바꾸지 않았다. 지침 변경은 평가 회귀를 함께 해야 한다. async·경계 검사는 type-aware 규칙(no-floating-promises, no-misused-promises, require-await, no-unsafe-*)이 맡는다.
- projectService 방식은 재지 않아 쓰지 않는다. tsconfig 프로그램 밖 TS 파일은 type-aware 작업에서 건너뛴다.
- `jsts.PackageModules`는 W15 탐색을 다시 쓴 것이다. 두 구현은 시험으로 묶여 있다.
- Windows 실행, 다른 eslint·TypeScript 계열, Bun·Deno는 확인하지 않았다.
