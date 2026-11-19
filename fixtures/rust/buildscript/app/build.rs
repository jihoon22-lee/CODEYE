fn main() {
    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rustc-cfg=from_build");
    let out = std::env::var("OUT_DIR").unwrap();
    std::fs::write(out + "/marker.txt", "ran").unwrap();
}
