// A nightly-only bench: the stable toolchain refuses #![feature] (E0554).
#![feature(test)]
extern crate test;

#[bench]
fn bench_total(b: &mut test::Bencher) {
    b.iter(|| nightlybench::total(&[1, 2, 3]));
}
