#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct Runtime Runtime;

/**
 * Queue one typed JSON UI action without blocking on vehicle work.
 * # Safety
 * `runtime` must be live; `request` must be a NUL-terminated UTF-8 string.
 */
bool runtime_submit(struct Runtime *runtime, const char *request);

/**
 * Attach a UI sink, or detach with NULL. Detachment waits for in-flight
 * callbacks, so the UI can safely destroy its context after this returns.
 * # Safety
 * `runtime` must be live. The callback must copy its borrowed JSON and return
 * immediately, without re-entering this ABI. `context` stays live until detach.
 */
void runtime_observe(struct Runtime *runtime, void (*callback)(void*, const char*), void *context);

/**
 * The build version of the core, stamped from `CARGO_PKG_VERSION` (same git
 * tag as `APP_VERSION` in harbour-electric-eel.pro). Static storage - do not
 * free.
 */
const char *core_version(void);

/**
 * Run the application lifecycle from the executable's forwarding entrypoint.
 *
 * # Safety
 * `arguments` must contain `argument_count` valid C argument strings and remain
 * live until this function returns, as required by Qt's application setup.
 */
int electric_eel_app_main(int argument_count, char **arguments);
