// eslint-disable-next-line @typescript-eslint/no-explicit-any -- the managed config ignores directives
export const loose: any = 1;

// @ts-expect-error tsc honors this directive and reports nothing for it
export const hidden: number = "x";
