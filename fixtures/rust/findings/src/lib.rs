//! Findings fixture of the W20 Rust pack: every defect below is intentional.
use std::collections::HashMap;

pub fn unused_var() -> i32 {
    let x = 5;
    let v: Vec<i32> = Vec::new();
    if v.len() == 0 {
        return 1;
    }
    0
}

pub fn ptr_arg(v: &Vec<i32>) -> usize {
    v.len()
}

/// Reads through a raw pointer.
pub unsafe fn danger(p: *const i32) -> i32 {
    *p
}

pub fn undocumented(p: *const i32) -> i32 {
    unsafe { *p }
}

pub fn panic_in_result(s: &str) -> Result<i32, String> {
    if s.is_empty() {
        panic!("empty");
    }
    Ok(1)
}

pub fn not_done() -> i32 {
    todo!()
}

pub fn wide() -> &'static str { "한글한글" } pub fn after() { let tail = 1; }

#[allow(unused_variables)]
pub fn allowed_rustc() {
    let hidden = 1;
}

#[allow(clippy::len_zero)]
pub fn allowed_clippy(v: &[i32]) -> bool {
    v.len() > 0
}
