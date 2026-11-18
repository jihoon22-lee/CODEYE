# 41. W16 C/C++ 팩: cppcheck와 clang-tidy

W16은 [분석기 팩 계약](39-r1-analyzer-packs.md)을 따르는 C/C++ 팩이다. 기준 분석기 **cppcheck**(`cpp.cppcheck`, 필수)와 추가 깊이 분석기 **clang-tidy**(`cpp.clang_tidy`)를 구현한다. Clang Static Analyzer(CSA)는 clang-tidy의 `clang-analyzer-*` 검사로만 실행한다. `clang --analyze`는 같은 엔진이므로 실행하지 않는다. 두 작업 모두 저장소 코드를 실행하지 않는다(`RepositoryCode=false`, §3.5).

검증 범위는 Linux(WSL2) x86-64에서 conda-forge 빌드를 실제로 실행한 결과다. cppcheck 2.22.0과 clang-tidy 23.1.5(LLVM 23.1.5)를 썼다. `--clang-tidy`로 준 `clang-tidy`는 `clang-tidy-23`을 가리키는 symlink다. 어댑터는 symlink를 풀어 실제 파일이 일반 파일인지 확인하고, 그 디렉터리를 PATH로 쓴다. envelope에는 계약상 실행 파일 경로가 없고 이름과 버전만 남는다. Windows는 단위 시험을 windows/amd64로 빌드해 WSL interop으로 실행한 것까지만 확인했다. Windows에서 실제 도구는 실행하지 않았다. CI에는 두 도구가 없다. 실제 도구 시험은 `CODEYE_TEST_CPPCHECK`·`CODEYE_TEST_CLANG_TIDY`가 있을 때만 실행한다. CI에서는 캡처한 실제 출력을 재생하는 시험이 같은 판정 경로를 검사한다(§9).

## 1. 구성 요약

| 부분 | 위치 | 역할 |
|---|---|---|
| C/C++ 팩 | `internal/worker/analysis/cpp` | 계획(`cpp.go`), compile DB 매핑(`compiledb.go`), cppcheck(`cppcheck.go`, `cppcheck_parse.go`), clang-tidy(`clangtidy.go`, `clangtidy_parse.go`), 저장 전 출력 정리(`sanitize.go`), 공통 실행(`common.go`) |
| 빌트인 연결 | `internal/worker/analysis/builtin/cpp.go` | `--cppcheck`·`--clang-tidy` 설정, 시작 probe, 카탈로그 항목(Go 다음) |
| 서술 | [cppcheck](../examples/analyzer-cppcheck.yaml) · [clang-tidy](../examples/analyzer-clang-tidy.yaml) | 실행 계약(`executes_repository_code: false`) |
| 실제 산출물 | [cppcheck](../examples/observations-cppcheck.json) · [clang-tidy](../examples/observations-clang-tidy.json) | fixture에 생성 헤더를 더한 사례(`with_generated_header`)의 envelope |
| fixture | `fixtures/w16-cpp/input/` | CMake 프로젝트. 다른 머신의 compile DB, 의도한 결함, 생성되는 `config.h`(입력에는 없음) |
| 기대값 | `fixtures/w16-cpp/expected.json` | 입력 밖에 둔 11개 사례의 판정·관찰·범위 |
| 중첩 측정 | `fixtures/w16-cpp/overlap/overlap.cpp` | 두 엔진의 같은 줄 보고와 변형(§7) |
| 캡처 | `internal/worker/analysis/cpp/testdata/` | 고정 버전의 stdout·stderr·종료 코드 |

팩 계약(pack, obsdoc, 보고서 decoder, 이력, schema, API)은 바꾸지 않았다. 공유 카탈로그와 시험의 기대 목록에 이 팩의 항목을 더했다. 시험 도우미 두 곳에는 추가만 했다. `run` 시험의 `packRun`은 올린 artifact 바이트를 담고, `report` 시험의 `unionManifest`는 honored 설정 파일을 manifest에 묶는다.

## 2. 기준 분석기와 추가 깊이

[§2.6](39-r1-analyzer-packs.md#26-필수required-규칙)에 따라 생태계마다 기준 분석기는 하나다. C/C++의 기준은 cppcheck다.

- **build flag 없이 실행된다.** cppcheck는 compiler가 아니다. 모르는 헤더와 매크로를 견디며 번역 단위를 읽는다. compile DB가 없는 스냅샷에서도 실행할 수 있는 C/C++ 도구는 이것 하나다. clang-tidy는 clang frontend가 단위를 compile하므로 build flag가 필요하다. 그런데 C/C++ 저장소는 대개 compile DB를 커밋하지 않는다. 만들려면 configure·cmake·bear처럼 저장소 코드를 실행해야 한다(§3.6). clang-tidy를 기준으로 두면 compile DB가 없는 대부분의 보고서가 그것만으로 `partial`이 된다.
- **설치 의존이 적다.** cppcheck는 단일 실행 파일과 자체 library 설정(`std.cfg` 등)으로 동작한다. 대상 toolchain의 system header나 resource dir과 맞출 필요가 없다. Linux 배포판과 Windows 설치본이 있다.
- **clang-tidy는 추가 깊이다.** 경로 민감 분석(CSA)과 bugprone 검사로 cppcheck가 놓치는 구성을 보고한다(§7). 실패·미지원은 보고서에 보이지만 판정 필수 작업 실패로 세지 않는다.

capability 광고: `--cppcheck`만 있는 노드도 deep-review를 광고한다. `--clang-tidy`만 있는 노드는 광고하지 않는다(`builtin.TestCppAnalyzersAdvertiseOnlyWithTheBaseline`). 경로가 파일이 아니면 `invalid_config`이고, 분석기는 멤버로 남아 작업이 `tool_unavailable`로 끝난다.

계획: 범위에 C/C++ 파일(`.c .cc .cpp .cxx .c++ .h .hh .hpp .hxx .h++`)이 하나라도 있으면 분석기마다 작업 하나(`cppcheck`, `clang-tidy`)를 계획한다. 헤더만 있어도 계획한다. 그 작업은 `unsupported`(`no_translation_units`)로 보인다. 이 팩이 멤버가 되면 `pack-gap.cpp`는 계획되지 않는다. 숨은 디렉터리, `testdata`, `vendor`, `node_modules` 아래의 파일은 대상이 아니다(`pack.SourceFiles`와 같은 규칙).

## 3. compile DB는 데이터

스냅샷의 `compile_commands.json`은 원래 build 머신에서 쓴 데이터다. 어댑터는 이를 읽어 작업 사본에 맞게 매핑한다. 그 결과는 task scratch에 CODEYE가 쓴 compile DB이며, 도구는 그것만 읽는다. 항목의 compiler는 실행하지 않는다. driver 이름은 언어 모드(C 또는 C++)만 정한다.

### 3.1 경로 매핑

- 원래 소스 루트는 추정한다. 항목 파일의 절대 경로 접두어 중 스냅샷에 실제로 있는 파일에 가장 많이 맞는 것을 고른다. 동률이면 더 긴 접미어가 맞는 쪽이다. 추정한 루트는 다른 머신의 절대 경로이므로 기록하지 않는다. 추정하지 못하면 `compile_db_root_not_inferred`다.
- `directory`·`file`·`-I`·`-isystem`·`-iquote`·`-idirafter`·`-include`·`-imacros`를 그 루트 아래의 스냅샷 경로로 바꾼다. 상대 경로는 항목의 `directory` 기준으로 푼다. 원래 build 디렉터리(예: `_build/gen`)도 같은 규칙으로 작업 사본 경로가 된다. 그 디렉터리가 스냅샷에 없으면 `include_dirs_not_in_snapshot`, 강제 include 파일이 없으면 `forced_include_files_not_in_snapshot`다.
- 루트 밖의 include 경로는 버린다(`include_paths_outside_snapshot`). `/usr/include` 같은 system 경로도 여기에 든다. 도구는 자기 system header를 쓴다.
- Windows 경로(`C:\…`, `\\server\…`)의 `directory`와 `command`는 Windows 규칙(CommandLineToArgvW)으로 나눈다. 단위 시험만 했다.

### 3.2 항목 제외(사유와 수)

| 사유 | 경우 |
|---|---|
| `entry_malformed` | `directory`·`file`·`command`/`arguments`가 없거나 타입이 틀림, 닫히지 않은 따옴표 |
| `directory_not_absolute` | `directory`가 절대 경로가 아님 |
| `file_outside_snapshot_root` | 파일이 추정 루트 밖(예: `/opt/vendor/zlib/inflate.c`) |
| `file_not_in_snapshot` | 루트 안이지만 스냅샷에 없음 |
| `data_directory` | 숨은·`testdata`·`vendor`·`node_modules` 아래 |
| `header_entry` | 헤더가 항목의 주 파일 |
| `not_cpp_source` | C/C++ 확장자가 아님 |
| `out_of_scope` | 작업 범위 밖 |
| `duplicate_entry` | 같은 파일의 두 번째 이후 항목(첫 항목만 씀) |
| `msvc_driver_unsupported`, `unsupported_driver` | `cl`·`clang-cl`, `nvcc`·형식이 맞지 않는 driver |
| `unsafe_argument` | 허용 목록을 지난 뒤에도 안전하지 않은 인자가 남은 항목(§8.1, 방어 계층) |

제외 수는 `target.properties.entries_excluded`(예: `duplicate_entry:1,file_outside_snapshot_root:1,header_entry:1`)와 제약 `compile_db_entries_excluded`·`duplicate_compile_db_entries`로 남는다. 범위 안 소스의 모든 항목이 제외되면 그 단위는 `compile_db_entry_excluded`, compile DB에 항목이 없으면 `not_in_compile_db`다. 두 경우 모두 처리 파일이 아니다. 둘 이상의 compile DB가 있으면 얕은 경로부터 읽고 `multiple_compile_dbs`를 남긴다. JSON이 아니거나 크기·항목 제한(64 MiB, 100,000 항목, 항목당 4,096 인자)을 넘으면 `compile_db_unreadable`로 실패한다. 아무것도 추측하지 않는다.

### 3.3 flag 허용 목록

남기는 인자는 분석 의미를 바꾸는 것뿐이다.

- `-D`·`-U`: 이름 형식을 검사하고 값은 1,024바이트 이하
- `-std=`: 형식 검사
- `-x`: `c`, `c++`, `c-header`, `c++-header`만
- `--target=`: `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`이고 붙여서 출력
- `-stdlib=`: `libc++`·`libstdc++`·`platform`만
- `-O`: 수준 하나
- 언어 방언을 바꾸는 `-f` 옵션(`-fno-exceptions`, `-fno-rtti`, `-fms-extensions`, `-funsigned-char` 등)
- `-m` target 옵션(`-m32`, `-march=…`, `-msse4.2` 등), `-ansi`·`-pthread`·`-nostdinc`·`-nostdinc++`
- 매핑한 include 경로

그 밖은 모두 버리고 범주별로 센다(`target.properties.flags_dropped`).

| 범주 | 예 | 제약 |
|---|---|---|
| `code_loading` | `-fplugin=`, `-fpass-plugin=`, `-Xclang -load`, `-Xanalyzer`, `-mllvm` | `code_loading_flags_dropped` |
| `response_file` | `@flags.rsp` | `response_files_dropped` |
| `driver_config`, `driver_mode`, `toolchain_path` | `--config=`, `--driver-mode=`, `-B`, `--sysroot`, `--gcc-toolchain`, `-resource-dir` | `compile_flags_dropped` |
| `modules`, `instrumentation`, `precompiled_header`, `backend_option`, `positional_argument`, `not_allowlisted` | `-fmodules`, `-fsanitize=`, `-include-pch`, `-Wa,` | `compile_flags_dropped` |
| 구조(세지만 보고하지 않음) | `-c`, `-o`, `-MD`, `-MF`, `-g`, `-W…`, `-Werror`, 링커 인자 | — |

compiler wrapper(`ccache`, `sccache`, `distcc`, `icecc`, `buildcache`, `colorgcc`)는 argv[0]에서 벗긴다(`compiler_wrappers_stripped`). 출력 driver는 C++이면 `clang++`, 아니면 `clang`이다. driver 이름에 `++`가 있거나, `-x c++`이거나, 확장자가 C++이면 C++이다. 저장소 DB의 driver 경로는 쓰지 않는다. 기록하는 것은 basename(`drivers`)뿐이다.

fixture의 compile DB는 6항목이다. 출력은 3단위(`src/parse.cpp`, `src/ring.c`, `src/util.cpp`)다. 버린 flag는 `code_loading:3,response_file:1`이다(`TestFixtureCompileDB`).

### 3.4 저장소가 도구를 바꾸는 경로(실측)

| 경로 | 측정 | 막는 방법 |
|---|---|---|
| cppcheck가 cwd의 `std.cfg`, `posix.cfg` 같은 `--library` 설정을 읽음 | 깨진 `std.cfg`를 cwd에 두면 cppcheck가 실패한다. 파일 디렉터리, `cwd/cfg/`, `cppcheck.cfg`, platform 파일은 cwd에서 읽지 않았다 | cwd는 task scratch의 빈 디렉터리 `cwd`다(작업 사본이 아님) |
| clang driver가 driver 경로 디렉터리의 `<triple>-<driver>.cfg`·`<driver>.cfg`를 읽음 | 저장소 상대 driver 경로라면 저장소 파일을 읽을 수 있다 | `--extra-arg-before=--no-default-config`, 출력 driver는 basename |
| 저장소 `.clang-tidy`(`Checks`, `WarningsAsErrors`, `HeaderFilterRegex`, `ExtraArgs`, `CheckOptions`) | `--config`가 있으면 아무것도 바뀌지 않는다 | inline `--config`로 모든 키 고정(§5) |
| `CPATH`·`C_INCLUDE_PATH`·`CCC_OVERRIDE_OPTIONS` 등 환경 변수 | — | 명시 환경 `PATH`(도구 디렉터리)·`LANG=C.UTF-8`만 전달(executor가 실행별 HOME·TMP를 더함) |

`repository_tool_config` 사례(실제 도구 전용)가 위 파일을 모두 저장소에 두고, 결과가 `with_generated_header`와 같음을 확인한다.

### 3.5 저장소 코드를 실행하지 않는 근거

| 작업 | `RepositoryCode` | 근거 |
|---|---|---|
| `cppcheck` | false | 소스를 읽어 분석할 뿐 compile·link·실행하지 않는다. compile DB는 CODEYE가 다시 쓴 것이고, cppcheck는 그 flag를 데이터로 읽는다. library 설정은 cppcheck 설치본의 것이다(cwd는 빈 scratch). `--inline-suppr`·`--suppressions-list`·`--addon`·`--rule-file`은 쓰지 않는다. |
| `clang-tidy` | false | clang frontend가 단위를 parse하고 분석할 뿐 code generation·link·실행은 없다. plugin·`-load`·`-fplugin`·response file·driver 설정은 허용 목록이 막는다(§3.3, §8.1). 저장소 `.clang-tidy`는 읽지 않는다(`--config`). clang-tidy plugin(`--load`)은 쓰지 않는다. |

strace(`-f`, process 생성과 쓰기 계열 호출)로 측정한 결과, 두 도구는 자식 process나 thread를 만들지 않았고 파일도 쓰지 않았다. cppcheck build dir(`--cppcheck-build-dir`)과 clang-tidy cache는 쓰지 않는다. 그래서 executor 정리 확인 뒤에 남는 자손이 없다([docs/39 §11](39-r1-analyzer-packs.md#11-남은-제한과-다음-팩을-위한-메모)).

### 3.6 compile DB 생성은 구현하지 않는다

compile DB를 만들려면 configure·cmake·bear처럼 저장소의 build 설정을 실행해야 한다([docs/05](05-source-and-environments.md)). 이 단계는 구현하지 않았다.

- compile DB가 없으면 clang-tidy 작업은 프로세스를 시작하지 않고 `unsupported`(`compile_db_missing`, 제약 `compile_db_generation_not_implemented`)로 끝난다. 처리 파일은 0이다.
- cppcheck는 **축소 모드**로 실행한다. 범위 안 번역 단위를 파일 목록(`--file-list`)으로 주고 include 경로와 매크로를 주지 않는다. 제약 `compile_db_missing`·`reduced_mode_no_build_flags`, `target.properties.mode=reduced`, 단위 상태 `analyzed_*_without_build_flags`로 그 사실을 남긴다. 이 결과는 "C++ 정밀 분석 완료"가 아니다. include 디렉터리가 여러 개인 프로젝트는 사용자 헤더를 찾지 못해 `missing_include_files`로 실패한다(`without_compile_db`). include 경로를 추측하지 않는 것은 의도한 결정이다.

## 4. 실행, 종료 코드, 결과

두 도구 모두 executor Runner로 argv·cwd(scratch의 빈 `cwd`)·명시 환경·제한시간(기본 30분)으로 실행한다. 위치 인자는 모두 작업 사본·scratch의 절대 경로다. 실행 기록의 cwd와 경로는 `[REDACTED]`로 남는다.

### 4.1 cppcheck

1. `cppcheck --version` → `Cppcheck 2.22.0`. 미인식이면 검사를 실행하지 않는다(`tool_version_unrecognized`). 시험한 계열은 `2.22`이고, 다른 계열은 `tool_version_untested`로 실행한다. XML의 `<cppcheck version>`이 `--version`과 다르면 형태 오류다.
2. `cppcheck --output-format=xmlv2 --enable=warning,performance,portability,information,missingInclude --check-level=normal --platform=native --error-exitcode=2 --project=<scratch>/work/compile_commands.json`. 축소 모드는 `--file-list=<scratch>/work/files.txt`다.

결과는 stderr의 XML(results version 2)과 stdout의 `Checking …` 진행 줄이다. 한 실행이 모든 단위를 본다(whole-program 검사). XML은 stdlib streaming decoder로 읽는다. 끝(`</results>`)이 없거나 알 수 없는 요소·속성이 있으면 형태 오류이며, 0건이 되지 않는다.

| 관측(2.22.0 실측) | 결과 |
|---|---|
| exit 0, `checkersReport`만 | succeeded / `success_without_findings`(`clean_unit`) |
| exit 2, 검사 레코드 | succeeded / `success_with_findings` |
| exit 2, `missingInclude`(사용자 헤더) | failed / `missing_include_files`(configuration_error), 그 단위는 `partial_missing_include`, 다른 관찰 보존 |
| `missingInclude`가 헤더 안(`file0` 없음) | 모든 단위가 `possibly_partial_missing_include`, failed / `missing_include_files` |
| exit 2, `missingIncludeSystem`만 | 성공 가능, 제약 `system_includes_not_parsed`(cppcheck는 system header 대신 library 설정을 씀) |
| exit 2, `syntaxError`·`internalError` 등 | failed / `translation_unit_errors`(`failed_parse`) |
| `toomanyconfigs`·`normalCheckLevelMaxBranches` 등 | 제약 `preprocessor_configurations_limited`·`check_level_branches_limited` |
| 단위가 `Checking` 줄에 없음 | failed / `translation_units_not_processed` |
| exit 1 + `cppcheck: error:`(사용법 오류, 목록 파일 없음), signal | failed / `cppcheck_failed` |
| exit 0인데 레코드 있음, exit 2인데 레코드 없음 | failed / `exit_status_mismatch` |
| XML 잘림·손상 | failed / `output_truncated`·`output_unparsed`(parse 보존) |
| timeout·취소·정리 불확실 | timed_out, cancelled, failed/`cleanup_uncertain` |

`--error-exitcode=2`는 레코드가 하나라도 있으면 exit 2다. 심각도와 무관하고 `missingInclude`·`missingIncludeSystem`·`syntaxError` 하나만 있어도 그렇다. 예외는 cppcheck가 실행마다 쓰는 `checkersReport` 요약이다. 이 레코드만 있으면 exit 0이다(측정). 그래서 종료 코드 대조에서 이 레코드는 세지 않는다. 범위 레코드는 관찰이 아니라 도구 오류·제약이다.

### 4.2 clang-tidy

1. `clang-tidy --version` → `LLVM version 23.1.5`. 시험한 계열은 `23.1`이다. 개발 빌드(`23.1.5git`)는 미인식이다.
2. 단위를 16개씩 묶어 실행한다(`clang-tidy.check.N`): `clang-tidy -p <scratch>/work --config=<inline YAML> --extra-arg-before=--no-default-config --use-color=false <단위 절대 경로>…`. `--`는 쓰지 않는다. 쓰면 fixed compile DB 모드가 되어 `-p`를 무시한다(측정).

결과는 stdout의 진단 흐름(`file:line:col: level: message [check]`과 note)과 stderr의 단위 진행·요약 줄이다. `--export-fixes` YAML도 같은 진단을 담지만 파일이라 기록된 실행 stream에 묶을 수 없으므로 쓰지 않았다. 컴파일 경고(`clang-diagnostic-*`)는 선택하지 않는다. `#pragma` 등으로 그래도 나온 것은 finding이 아니다(`compiler_warnings_ignored`).

stderr의 `W warnings and E errors generated.`에서 **E는 한 실행 안에서 누적된다.** `Error while processing F.`는 누적 수가 0보다 커진 뒤 모든 단위에 찍힌다(23.1.5 측정). 그래서 단위의 실패는 그 단위에서 누적 수가 늘었는지로 정한다. 단위가 하나인 실행에서는 진행 줄이 없으므로 오류 줄로 정한다.

| 관측(23.1.5 실측) | 결과 |
|---|---|
| exit 0, 경고만 | succeeded / `success_with_findings`(WarningsAsErrors는 빈 값으로 고정) |
| exit 0, 진단 없음 | succeeded / `success_without_findings` |
| exit 1, 한 단위의 사용자 헤더 `file not found` | failed / `missing_include_files`(`failed_missing_include`), 다른 단위의 관찰 보존 |
| exit 1, 구문·의미 오류 | failed / `translation_unit_errors`(`failed_compile`) |
| `Running without flags.`, compile DB 읽기 오류, `Error:`, crash·signal | failed / `clang_tidy_failed` |
| `Skipping F. Compile command not found.`, 단위 블록 없음 | failed / `translation_units_not_processed` |
| exit 0인데 오류 진단, exit 1인데 누적 오류 0 | failed / `exit_status_mismatch` |
| stdout 알 수 없는 줄·잘림 | failed / `output_unparsed`·`output_truncated` |
| compile DB 없음 | unsupported / `compile_db_missing`(프로세스 없음) |
| 범위 안 단위 0 | unsupported / `no_translation_units` |

실패한 작업은 처리 파일을 주장하지 않는다(`processed_paths=null`). 관찰은 남기고 `partial_parse`로 표시한다(W19와 같다).

## 5. 규칙 팩, 규칙 문맥, 억제

| | cppcheck `codeye-cpp-cppcheck` v1 | clang-tidy `codeye-cpp-clang-tidy` v1 |
|---|---|---|
| 선택 | `--enable=warning,performance,portability,information,missingInclude --check-level=normal --platform=native`. error 검사는 항상 켜져 있다. style은 실행하지 않는다. information·missingInclude는 범위 판단에만 쓴다 | `-*`, `clang-analyzer-*`(osx·optin·webkit·fuchsia·DeprecatedOrUnsafeBufferHandling·§7 중복 제외), `bugprone-*`(이름·형식·변환 계열과 §7 중복 제외), `performance-*`(avoid-endl, enum-size 제외), `concurrency-thread-canceltype-asynchronous`, `misc-redundant-expression`, `misc-unconventional-assign-operator`, `misc-throw-by-value-catch-by-reference`. 고정 키: `WarningsAsErrors: ""`, `HeaderFilterRegex: .*`, `ExcludeHeaderFilterRegex: ""`, `SystemHeaders: false`, `FormatStyle: none`, `CheckOptions: {}` |
| `repository_config` | compile DB 모드는 `honored`, `config_files`=compile DB(manifest SHA-256). 축소 모드는 `none` | `honored`, `config_files`=compile DB. 저장소 `.clang-tidy`는 `--config`가 대체한다(`repository_clang_tidy_config=overridden`, 측정) |
| `source_suppressions` | `disabled`. `--inline-suppr`가 없으면 `// cppcheck-suppress`가 효력이 없다(fixture `util.cpp:22`의 `mismatchAllocDealloc`가 보고됨) | `honored`. NOLINT·NOLINTNEXTLINE·NOLINTBEGIN을 보고하거나 끌 옵션이 23.1.5에 없다. stderr의 `Suppressed N warnings (K NOLINT)`에서 K를 `nolint_suppressed`로 세고 제약 `nolint_suppressed_diagnostics`를 남긴다 |
| `unreported_suppression_rules` | `[]` | `[]`(`honored`에는 쓰지 않음) |

compile DB가 바뀌면 설정 digest가 바뀐다. 따라서 이전 run과의 비교는 `rule_context_changed`가 된다. clang-tidy는 `honored`이므로 W23 비교에서 사라진 clang-tidy finding은 언제나 `absent_unverified`(`source_suppressions_hidden`)다. NOLINT를 추가해 숨긴 것과 고친 것을 가를 수 없기 때문이다([docs/39 §5.1](39-r1-analyzer-packs.md#51-규칙-문맥과-소스-안-억제-builder-4-tc-104)). cppcheck는 억제를 끄므로 같은 compile DB이면 사라진 finding이 `resolved_observed`가 될 수 있다.

범주 정책은 검사의 주제이며 영향 판정이 아니다. cppcheck: severity performance → performance, memleak·resourceLeak·mismatchAllocDealloc·doubleFree 계열 → resource_lifecycle, except·throw 계열 → error_handling, style → maintainability, 나머지 → correctness. clang-tidy: `performance-*` → performance, `clang-analyzer-security.*`와 몇 bugprone → security, Malloc·NewDelete·MismatchedDeallocator 계열 → resource_lifecycle, `concurrency-*` 계열 → concurrency, 예외·반환값 계열 → error_handling, `deadcode` → maintainability, 나머지 → correctness. 원문 severity는 그대로 둔다. 모든 관찰은 검토 전의 낮은 등급 도구 finding이다.

## 6. 번역 단위 범위와 생성 헤더

단위(`coverage.unit=translation_unit`)는 compile DB 모드에서 매핑된 단위, 축소 모드에서 범위 안 소스 파일이다.

| 상태 | 뜻 | 처리 파일 |
|---|---|---|
| `analyzed_clean`, `analyzed_with_findings` | 끝까지 분석함 | 예 |
| `analyzed_clean_without_build_flags`, `analyzed_with_findings_without_build_flags` | 축소 모드에서 분석함 | 예(성공한 작업만) |
| `partial_missing_include` | cppcheck가 사용자 헤더 없이 분석함 | 아니오 |
| `possibly_partial_missing_include` | 헤더 안의 missingInclude는 포함한 단위 없이 한 번만 보고됨(측정) | 아니오 |
| `failed_missing_include`, `failed_compile`, `failed_parse` | clang-tidy 컴파일 실패, cppcheck 구문 실패 | 아니오 |
| `not_processed`, `unknown` | 출력이 처리를 보여 주지 않음, 실행이 끝나지 않음 | 아니오 |
| `not_in_compile_db`, `compile_db_entry_excluded` | compile DB에 없음, 모든 항목 제외 | 아니오 |

**생성 헤더.** fixture의 `src/parse.cpp`는 configure가 build 디렉터리에 만드는 `config.h`를 include한다. 커밋된 상태(`as_committed`)에서 두 도구는 모두 `missing_include_files`(configuration_error)로 실패한다. cppcheck는 그 단위를 `partial_missing_include`로, clang-tidy는 `failed_missing_include`로 둔다. 찾지 못한 헤더는 도구 오류 `missing_include src/parse.cpp:5`이며 결함이 아니다. 다른 두 단위의 관찰은 남지만 처리 파일은 주장하지 않는다. 같은 헤더를 compile DB의 `-I`가 가리키는 `_build/gen/config.h`에 두면 두 도구 모두 성공한다(`with_generated_header`). 처리 파일은 세 단위다. 헤더(`include/w16/ring.h`)의 관찰은 남지만 헤더를 처리 파일로 주장하지 않는다.

## 7. 엔진 중복

cppcheck와 clang-tidy는 서로 다른 엔진이다. cppcheck는 자체 parser와 value flow를 쓰고, clang-tidy는 clang frontend의 AST matcher와 CSA 기호 실행을 쓴다. 한쪽이 다른 쪽을 감싸지 않으므로 둘 다 실행한다. CSA는 clang-tidy 안에서만 실행한다. [W19 §9](39-r1-analyzer-packs.md#9-go-vet과-staticcheck-서로-다른-엔진과-중복)와 같은 규칙으로, 기준 분석기가 같은 줄에서 함께 보고한 것만 보고한 clang-tidy 검사는 선택에서 뺐다. 측정한 변형 중 clang-tidy만 보고한 것이 있으면 그 검사는 남겼다. clang-tidy 안에서는 Analyzer 검사가 bugprone 검사와 같은 것만 보고하면 뺐다. 별칭 계열(`cert-*`, `hicpp-*`, `cppcoreguidelines-*`)은 같은 진단을 두 이름으로 보고하므로 선택하지 않는다.

측정: `fixtures/w16-cpp/overlap/overlap.cpp`(C++17, 한 함수에 한 구성, 뒤쪽은 변형)에서 두 도구를 최종 선택으로 실행했다(`TestRealEngineOverlap`). 시험은 아래 판단을 고정한다.

| 제외한 clang-tidy 검사 | 같은 줄에서 함께 보고한 cppcheck 검사(줄) |
|---|---|
| `bugprone-assert-side-effect` | assignmentInAssert(17, 115). 함수 호출이 든 assert(203)는 둘 다 보고하지 않음 |
| `bugprone-suspicious-memset-usage` | memsetZeroBytes(21, 204), memsetValueOutOfRange(156). memsetZeroBytes(19)는 cppcheck만 보고 |
| `clang-analyzer-core.BitwiseShift` | shiftTooManyBits·integerOverflow(42, 122, 호출 경유 포함), shiftNegative(202) |
| `clang-analyzer-core.StackAddressEscape` | returnDanglingLifetime(53, 201), danglingLifetime(129), autoVariables(164) |
| `clang-analyzer-cplusplus.InnerPointer` | stlcstr·returnDanglingLifetime(77), invalidContainer(151, 180) |
| `clang-analyzer-cplusplus.Move` | (clang-tidy 안) bugprone-use-after-move가 같은 줄을 모두 보고(27, 207, 208). 반대로 bugprone만 보고한 줄이 있음(206) |
| `clang-analyzer-security.FloatLoopCounter` | (clang-tidy 안) bugprone-float-loop-counter와 같은 규칙(90) |

| 남긴 clang-tidy 검사 | 함께 보고한 줄 | clang-tidy만 보고한 변형 |
|---|---|---|
| `bugprone-unused-return-value` | 35, 80 | `std::error_code`를 돌려주는 함수(160) |
| `clang-analyzer-unix.MismatchedDeallocator` | 46 | 다른 함수에서 할당한 `new[]`(126) |
| `clang-analyzer-security.ArrayBound` | 58 | 호출 경유 인덱스(131). 반대로 fixture `parse.cpp:14`의 범위 밖 쓰기는 cppcheck만 보고 |
| `clang-analyzer-core.DivideZero` | 63, 134, 166, 192 | 구조체 field를 거친 0(189) |
| `clang-analyzer-core.NullDereference` | fixture `ring.c:23` | 구조체 field의 null(193). 두 번 호출을 거친 null(195)은 cppcheck만 보고 |
| `clang-analyzer-unix.Malloc` | 120, 196, 197, fixture `ring.c:12` | 다른 함수에서 해제한 뒤 다시 해제(199) |
| `bugprone-unhandled-self-assignment` | 68, 140 | `unique_ptr` field의 복사 대입(170) |
| `bugprone-standalone-empty` | 80, 154 | 사용자 class의 `empty()`(184) |
| `bugprone-branch-clone` | 106(cppcheck identicalInnerCondition은 107, 같은 구성의 다른 줄) | 같은 then·else(186). cppcheck duplicateBranch는 style이라 실행하지 않음 |
| `bugprone-use-after-move` | 208(cppcheck accessMoved) | `std::string`(27), 일반 struct(206), 분기 안 move(207) |

그 밖에 clang-tidy만 보고한 구성: suspicious-semicolon(31), suspicious-realloc-usage(38), unique-ptr-array-mismatch(49), cplusplus.NewDelete(70), suspicious-string-compare(83), integer-division(87), float-loop-counter(90), misplaced-operator-in-strlen-in-alloc(96), insecureAPI.strcpy(97), inaccurate-erase(101). cppcheck만 보고한 구성: memsetZeroBytes(19), pointerSize(23). 원인을 판정하지 않고 측정 결과만 적는다.

남은 같은 줄 보고는 각 작업의 관찰로 남는다. 보고서는 도구 사이에서 자동 병합하지 않는다(TC-102). fixture `ring.c:12`의 memleak과 unix.Malloc은 두 finding이다(`report.TestAnalyzerDecoderCppPack`). 처리 파일은 합집합으로 센다. 측정은 이 fixture의 구성에 한정된다. 다른 코드에서 제외한 검사가 cppcheck가 놓치는 것을 보고할 수 있다. 그 경우는 규칙 팩 버전을 올려 다시 고른다.

## 8. 보안

대상 저장소의 `compile_commands.json`과 소스는 적대적 입력으로 본다.

### 8.1 compile DB 값으로 인자 주입(수정)

clang은 `@`로 시작하는 argv 원소를 응답 파일로 펼친다. 그러면 남긴 flag의 값(`-x <v>`, `-target <v>`)을 통해 `-Xclang -load <plugin>`, `-fplugin=`, `-B` 같은 인자가 들어올 수 있다. 수정 내용:

- 남기는 값은 모두 고정 집합이나 엄격한 형식만 통과한다(§3.3). `--target`은 붙여 쓴다. `@`로 시작하는 원소는 어느 위치든 남지 않는다.
- 코드 적재·toolchain 변경·driver 설정 flag는 허용 목록 밖이다. 그래도 한 단위에 남으면 `Unit.safe()`가 그 항목을 `unsafe_argument`로 제외한다. `RenderDB`는 driver 다음의 모든 원소가 옵션이거나, 절대 경로이거나, 언어 값인지 다시 검사한다.
- 시험 `TestCompileDBValuesCannotInjectArguments`는 `@file` 값을 각 위치(`-x`, `-target`, `--target=`, `-D`, `-U`, `-std=`, `-stdlib=`, `-O`, `-I`, `-isystem`, `-include`, `-imacros`, 단독, `-Xclang`, `-mllvm`, `--config=`, `-B`, driver)에 넣는다. 출력 argv에는 `@` 원소도, 안전하지 않은 옵션도 없다.

### 8.2 옵션처럼 보이는 저장소 경로(수정)

저장소에서 온 위치 인자는 모두 옵션으로 해석될 수 없어야 한다. 주 파일, include 경로, `-p`, `--project=`, `--file-list=`와 그 목록의 각 줄은 모두 작업 사본·scratch의 절대 경로다. compile DB 매핑은 항목의 주 파일을 옵션 검사보다 먼저 알아본다. 그래서 `-x.c`, `--help.cpp`가 flag로 버려지지 않고 파일로 분석된다. 사례 `option_like_names`·`option_like_names_reduced`가 이를 확인하고, `TestOptionLikeFileNamesStayPaths`는 argv를 검사한다. clang-tidy의 `--`는 `-p`를 무시하게 만드므로 쓰지 않는다(§4.2).

### 8.3 작업 사본 밖 파일 내용의 저장(수정)

저장소는 `#include "/etc/…"`이나 `..`로 운영자 계정이 읽을 수 있는 파일을 도구가 읽게 만들 수 있다. 진단은 그 내용을 옮길 수 있다. 측정 결과, clang-tidy는 진단마다 소스 줄(snippet)을 찍는다. `-fno-caret-diagnostics`와 `-fno-diagnostics-show-line-numbers`는 이를 없애지 못했다. `--extra-arg=-fno-caret-diagnostics`를 주면 stderr의 `… generated.` 줄이 사라져 단위 판정이 불가능해진다. 작업 사본 밖 파일의 오류는 header filter와 무관하게 언제나 출력된다. cppcheck XML에는 소스 줄이 없지만 메시지와 `<symbol>`에 그 파일의 이름·token이 들어간다.

그래서 실행 결과를 기록하기 전에 stream을 고친다(`sanitize.go`).

- **clang-tidy stdout**: 모든 snippet 줄을 지운다(`source_snippets=removed`). 위치가 작업 사본 밖인 진단은 note와 함께 지운다. 남긴 진단의 밖 note도 지운다.
- **cppcheck stderr**: 위치 하나라도 작업 사본 밖인 `<error>` 레코드를 지운다. id·severity·단위만 판정에 쓴다.
- **판정**: 지운 오류는 메시지 없이 도구 오류(`compile_error`·`parse_error`, "message withheld")로 남는다. 그 단위는 실패한다. 수는 `target.properties.outside_records_dropped`와 제약 `outside_workspace_diagnostics_dropped`로 남는다. 종료 코드 대조는 지운 레코드도 센다.
- **허용하는 경로**: 작업 사본과 clang 의사 파일(`<command line>`, `<built-in>`)만이다. system header 위치의 진단도 지운다. `SystemHeaders: false`이므로 남는 것은 오류뿐이다.

시험: `TestRealHostFileContentIsNotStored`(compile DB·축소 모드)와 `run.TestCppPackHostFileNotStored`(워커 end-to-end, 올린 모든 artifact와 보고서)는 운영자 임시 디렉터리에 고유 canary 문자열이 든 파일을 두고 절대 경로로 include한다. canary는 저장 stream, envelope, task 결과, 보고서 어디에도 없다. `TestSanitizeStreams`는 CI에서 정리 규칙을 검사한다. 모든 실제 사례는 저장 stream에 snippet 줄이 없음을 확인한다.

### 8.4 남은 위험

- **도구는 운영자 계정이 읽을 수 있는 모든 파일을 읽을 수 있다.** 막는 것은 저장뿐이다. OS sandbox는 범위 밖이다. 신뢰 경계가 다른 저장소는 별도 계정이나 노드에서 분석한다([docs/08 §2](08-native-execution.md#2-신뢰와-실행-정책)).
- **작업 사본 안 위치의 진단도 밖 파일에서 계산한 값을 담을 수 있다.** 측정 예: 작업 사본의 파일에서 `#embed`로 밖 파일을 읽고 `_Static_assert`로 비교하면, 진단 메시지에 `expression evaluates to '67 == 0'`처럼 그 파일의 바이트 값이 나온다. 밖 헤더의 매크로가 작업 사본 줄에서 펼쳐지는 경우도 같다. 위치 기준 정리로는 이를 막지 못한다.
- 이 정리는 C/C++ 팩의 것이다. executor의 stream 저장 규칙은 바꾸지 않았다.

## 9. 시험과 실행 근거

실제 도구 출력 캡처(redacted stdout·stderr·exit code, `internal/worker/analysis/cpp/testdata/`)는 다음 명령으로 만들었다. 기대값은 `fixtures/w16-cpp/expected.json`이다. `CODEYE_W16_PRINT=<file>`을 주면 비교하지 않고 실제 결과를 줄마다 기록한다(기대값 갱신 검토용).

```text
CODEYE_TEST_CPPCHECK=$HOME/.local/share/codeye-tools/cpp/bin/cppcheck \
CODEYE_TEST_CLANG_TIDY=$HOME/.local/share/codeye-tools/cpp/bin/clang-tidy \
CODEYE_W16_CAPTURE=$PWD/internal/worker/analysis/cpp/testdata \
go test -count=1 -run TestRealFixtureCases ./internal/worker/analysis/cpp/
```

[실제 산출물 예시](../examples/observations-cppcheck.json)는 같은 실행에 `CODEYE_W16_DUMP=<dir>`을 주어 얻은 `with_generated_header` envelope다. `scripts/validate_bundle.py`의 `check_w16_contracts`는 서술·예시·기대값을 검사한다. 예시 관찰의 blob SHA-256은 커밋한 fixture 바이트에, compile DB digest는 커밋한 DB에 묶여 있다. 예시가 기대값과 같은지도 확인하고, 음성 사례 11개가 거부되는지 본다.

| 시험 | 범위 | CI |
|---|---|---|
| `cpp.TestRealFixtureCases` | 11개 사례: 커밋 상태(생성 헤더 없음), 생성 헤더, 두 단위 실패, 단위별 batch, compile DB 없음, 자족 축소 모드, 결함 없는 단위(두 모드), 옵션 같은 파일 이름(두 모드), 저장소 도구 설정. 원본·스냅샷 불변, 노드 경로 미노출, cwd·환경·argv 형식, snippet 미저장 | 건너뜀 |
| `cpp.TestRealHostFileContentIsNotStored` | 밖 파일 canary 미저장(§8.3) | 건너뜀 |
| `cpp.TestRealEngineOverlap` | §7 표 | 건너뜀 |
| `cpp.TestRealProbes` | 실제 시작 probe와 probe 디렉터리 정리 | 건너뜀 |
| `cpp.TestReplayFixtureCases`, `TestCapturesHoldNoNodePaths` | 캡처 바이트 재생으로 실제 도구 전용이 아닌 사례 10개의 판정, 캡처에 노드 경로 없음 | 실행 |
| `cpp.TestReplayFailuresAreNeverSuccess` | 잘림, 사용법 오류, exit 0인데 레코드, XML 절단, timeout, 취소, 정리 불확실, 미처리 단위, 플래그 없이 실행, crash, 오류 없는 exit 1, 알 수 없는 줄, 단위 블록 누락 | 실행 |
| `cpp.TestReplayVersionContract`, `TestReplayWorkspaceDrift`, `TestHeadersOnlyIsNotAPass` | 다른 계열(`tool_version_untested`), 미인식 버전, 작업 사본 변경, 헤더만 있는 범위 | 실행 |
| `cpp.TestCompileDB*`, `TestSplitCommand`, `TestFixtureCompileDB`, `TestCompileDBValuesCannotInjectArguments`, `TestOptionLikeFileNamesStayPaths` | §3·§8.1·§8.2 | 실행 |
| `cpp.TestPlan`, `TestDescriptors`, `TestClangTidyRulePack`, `TestVersions`, `TestParsersRejectDamagedOutput`, `TestSanitizeStreams`, `TestExpectedFixtureIsComplete` | 계획 key·필수 여부, 규칙 팩, 버전 parser, 손상 출력, 정리 규칙, 기대값 형식 | 실행 |
| `builtin.TestCppAnalyzersAdvertiseOnlyWithTheBaseline` | 기준 분석기 광고, 잘못된 경로, probe 형식(뒷부분의 실제 probe는 도구 필요) | 일부 |
| `run.TestCppPackWithoutTools` (Linux) | 실제 워커: 두 작업 계획·`tool_not_configured`·not-run 행·`pack-gap.cpp` 없음 | 실행 |
| `run.TestCppPackWithRealTools`, `run.TestCppPackHostFileNotStored` (Linux) | 실제 워커 end-to-end, 보고서 decoder, 규칙 문맥 행, 밖 파일 미저장 | 건너뜀 |
| `report.TestAnalyzerDecoderCppPack` | 실제 envelope 두 개의 보고서: 필수 여부, 도구 행, 규칙 문맥, 같은 줄 두 finding, 합집합, 코드 제외 export | 실행 |

## 10. 남은 제한

- compile DB 생성(configure·cmake·bear)을 구현하지 않았다. compile DB가 없는 프로젝트는 clang-tidy가 미지원이고, cppcheck는 축소 모드다(§3.6).
- 원래 루트 추정은 파일 경로 접미어에 기댄다. 소스를 build 중에 복사·생성하는 프로젝트는 매핑하지 못할 수 있다. 생성 소스와 생성 헤더는 스냅샷에 없으면 범위 공백이다.
- 단위마다 compile DB 항목 하나만 쓴다. 같은 파일을 다른 매크로로 compile하는 두 번째 이후 항목은 분석하지 않는다(`duplicate_compile_db_entries`). cppcheck는 `--project`가 준 설정만 확인하고(`preprocessor_configs_checked`), 다른 `#ifdef` 조합은 확인하지 않는다.
- target은 compile DB 값 또는 노드 기본값이다. cross target의 system header는 도구가 가진 것으로 해석한다. MSVC·nvcc 항목은 분석하지 않는다.
- cppcheck의 CTU와 clang-tidy CSA는 한 실행 안의 단위만 본다. clang-tidy는 16단위씩 나누므로 단위 사이 분석은 하지 않는다.
- 중첩 판단은 한 fixture의 측정이다(§7).
- NOLINT 억제를 보고할 수 없으므로 clang-tidy finding은 비교에서 해결되지 않는다(§5).
- 밖 파일 내용의 저장 방지는 위치 기준이다. 계산된 값의 누출이 남는다(§8.4).
- Windows에서 실제 도구를 실행하지 않았다. Windows compile DB의 분해와 경로 매핑은 단위 시험까지만 확인했다.
