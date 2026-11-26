/// Sums the values; `unused` is a rustc warning on shipped code.
pub fn total(values: &[u32]) -> u32 {
    let unused = values.len();
    values.iter().sum()
}

/// Reports an empty slice; `len() == 0` is a clippy lint on shipped code.
pub fn empty(values: &[u32]) -> bool {
    values.len() == 0
}
