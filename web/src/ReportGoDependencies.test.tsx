// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { ReportGoDependencies } from './ReportGoDependencies';
import type { GoPackageGraph } from './parseReportSections';

afterEach(cleanup);

it('lists a package whose external tests import the selected package', () => {
  const a = 'example.test/module/a', b = 'example.test/module/b';
  const graph: GoPackageGraph = { task: 'go-vet', module: 'example.test/module', scope: 'selected_packages',
    status: 'partial', platform: 'linux/amd64', parsed: 3, ignored: 0, incomplete: 0, unresolved: 0, unparsed: 0,
    limitations: ['go_package_scope_limited'], packages: [a,b],
    edges: [{ from: b, to: a, kind: 'external_test', path: 'b/use_test.go', line: 4 }],
  };
  render(<ReportGoDependencies graphs={[graph]} />);
  const rows = screen.getAllByRole('row');
  expect(within(rows[1]).getAllByRole('cell')[3].textContent).toBe('1');
  expect(within(rows[2]).getAllByRole('cell')[3].textContent).toBe('0');
  fireEvent.click(within(rows[1]).getByRole('button'));
  expect(screen.getByText('b/use_test.go:4')).toBeTruthy();
  expect(screen.getByText('외부 테스트 import')).toBeTruthy();
  expect(screen.queryByText(/패키지 목록에 없는 Go 파일/)).toBeNull();
  expect(screen.queryByText(/운영 코드 import에서 순환을 찾지 못했습니다/)).toBeNull();
});

it('keeps absent candidate files distinct from ignored variants and read failures', () => {
  const graph: GoPackageGraph = { task: 'go-vet', module: 'example.test/module', scope: 'all_module_packages',
    status: 'partial', platform: 'linux/amd64', parsed: 732, ignored: 22, unlisted: 2, incomplete: 0, unresolved: 0, unparsed: 0,
    limitations: ['go_candidate_files_unlisted'], packages: ['example.test/module/a'], edges: [],
  };
  render(<ReportGoDependencies graphs={[graph]} />);
  expect(screen.getByText(/읽은 Go 파일 732개 · 다른 빌드 구성 파일 22개.*패키지 목록에 없는 Go 파일 2개/)).toBeTruthy();
  expect(screen.queryByText(/읽지 못한 파일 2개/)).toBeNull();
});
