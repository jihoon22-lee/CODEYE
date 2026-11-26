// Checks which activity the crate reports as current inside one and two nested synchronous scopes.
use devenv_activity::{Activity, ActivityStart, current_activity_id};

fn main() {
    let outer = Activity::operation("outer").start();
    let inner = Activity::operation("inner").start();
    let mut failed = 0;

    let none = current_activity_id();
    println!("outside any scope: {none:?}");
    if none.is_some() {
        println!("SCOPE ORDER CHECK FAILED: an activity is current outside every scope");
        failed += 1;
    }

    let single = outer.with_new_scope_sync(current_activity_id);
    println!("one scope (outer={}): {single:?}", outer.id());
    if single != Some(outer.id()) {
        println!("SCOPE ORDER CHECK FAILED: single scope reports {single:?}, expected {}", outer.id());
        failed += 1;
    }

    let (nested, after_inner) = outer.with_new_scope_sync(|| {
        let nested = inner.with_new_scope_sync(current_activity_id);
        (nested, current_activity_id())
    });
    println!("nested scope (inner={}): {nested:?}; back in outer: {after_inner:?}", inner.id());
    if nested != Some(inner.id()) {
        println!(
            "SCOPE ORDER CHECK FAILED: nested scope reports {nested:?}, expected the inner activity {}",
            inner.id()
        );
        failed += 1;
    }
    if after_inner != Some(outer.id()) {
        println!("SCOPE ORDER CHECK FAILED: after the nested scope {after_inner:?}, expected {}", outer.id());
        failed += 1;
    }

    if failed > 0 {
        std::process::exit(1);
    }
    println!("scope order check passed");
}
