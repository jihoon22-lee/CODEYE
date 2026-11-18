pub fn value() -> i32 {
    let unused_in_app = 1;
    macros::answer!()
}

#[cfg(from_build)]
pub fn built() -> bool {
    true
}
