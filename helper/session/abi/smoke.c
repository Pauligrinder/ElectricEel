/* Exercise the real C ABI without Bluetooth; runnable under the SDK's sb2. */
#include "tesla_ble.h"
#include <assert.h>
#include <pthread.h>
#include <string.h>
#include <time.h>

struct state {
    pthread_mutex_t lock;
    pthread_cond_t changed;
    uint64_t expected;
    uint32_t status;
    int completed;
    int stopped;
};

static void notify(void *context, const ee_message *message)
{
    struct state *state = context;
    assert(message->size == sizeof(*message));
    pthread_mutex_lock(&state->lock);
    if (message->kind == EE_COMPLETE) {
        assert(message->request == state->expected);
        assert(!state->completed);
        state->status = message->status;
        state->completed = 1;
    } else if (message->kind == EE_STOPPED) {
        assert(!state->stopped);
        state->stopped = 1;
    } else {
        assert(message->kind == EE_DIAGNOSTIC);
    }
    pthread_cond_broadcast(&state->changed);
    pthread_mutex_unlock(&state->lock);
}

static void wait_for(struct state *state, int *flag)
{
    struct timespec deadline;
    assert(clock_gettime(CLOCK_REALTIME, &deadline) == 0);
    deadline.tv_sec += 10;
    pthread_mutex_lock(&state->lock);
    while (!*flag)
        assert(pthread_cond_timedwait(&state->changed, &state->lock, &deadline) == 0);
    pthread_mutex_unlock(&state->lock);
}

int main(void)
{
    assert(ee_abi_version() == 2);
    assert(ee_session_stop(0) == EE_INVALID_HANDLE);
    uint8_t scalar[32] = {0};
    scalar[31] = 1;
    const char *vin = "5YJ3E1EA7KF000001";
    ee_config config = {
        .size = sizeof(config), .version = 2,
        .vin = {(const uint8_t *)vin, strlen(vin)},
        .private_scalar = {scalar, sizeof(scalar)}
    };
    struct state state = {
        .lock = PTHREAD_MUTEX_INITIALIZER,
        .changed = PTHREAD_COND_INITIALIZER,
        .expected = 1
    };
    uint64_t handle = 0;
    assert(ee_session_create(&config, notify, &state, &handle) == EE_OK);
    assert(handle != 0);
    memset(scalar, 0, sizeof(scalar));
    assert(ee_session_destroy(handle) == EE_INVALID); /* stop required */
    ee_request request = {
        .size = sizeof(request), .operation = EE_DISCONNECT,
        .id = 1, .timeout_ms = 1000
    };
    assert(ee_session_submit(handle, &request) == EE_OK);
    wait_for(&state, &state.completed);
    assert(state.status == EE_OK);
    assert(ee_session_submit(handle, &request) == EE_INVALID); /* reused ID */
    pthread_mutex_lock(&state.lock);
    state.expected = 2;
    state.completed = 0;
    pthread_mutex_unlock(&state.lock);
    request.id = 2;
    request.operation = EE_BODY_CONTROLLER_STATE;
    assert(ee_session_submit(handle, &request) == EE_OK);
    wait_for(&state, &state.completed);
    assert(state.status == EE_NOT_CONNECTED);
    assert(ee_session_stop(handle) == EE_OK);
    assert(ee_session_stop(handle) == EE_OK);
    request.id = 3;
    assert(ee_session_submit(handle, &request) == EE_STOPPING);
    wait_for(&state, &state.stopped);
    assert(ee_session_destroy(handle) == EE_OK);
    assert(ee_session_stop(handle) == EE_INVALID_HANDLE);
    pthread_cond_destroy(&state.changed);
    pthread_mutex_destroy(&state.lock);
    return 0;
}
