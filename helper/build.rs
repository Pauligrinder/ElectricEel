// Regenerates the C header for the app-facing ABI in `ffi.rs` whenever the
// crate is built. The header is consumed by app/src/teslaclient.cpp (via the
// include path wired in app/harbour-electric-eel.pro). cbindgen runs on the
// host (it's parse-only); only the resulting header is used by the target
// cross-compile.
fn main() {
    let crate_dir = std::env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR set by cargo");
    let out = std::path::Path::new(&crate_dir).join("electriceelcore.h");
    cbindgen::Builder::new()
        .with_crate(crate_dir)
        .with_language(cbindgen::Language::C)
        .generate()
        .expect("cbindgen should generate the union header")
        .write_to_file(out);
    println!("cargo:rerun-if-changed=src/ffi.rs");
}
