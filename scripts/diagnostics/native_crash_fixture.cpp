// Disposable child only: never pointed at a real game/player.
#include <windows.h>
#include <cstdio>
#include <cstring>
__declspec(noinline) void crash_leaf() { *reinterpret_cast<volatile int*>(0) = 42; }
__declspec(noinline) void crash_middle() { crash_leaf(); }
__declspec(noinline) void crash_root() { crash_middle(); }
int main() {
    SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX);
    std::puts("ready"); std::fflush(stdout);
    char input[40];
    while (std::fgets(input, sizeof(input), stdin)) {
        if (std::strncmp(input, "handled", 7) == 0) {
            __try { crash_root(); } __except(EXCEPTION_EXECUTE_HANDLER) { std::puts("handled"); }
        } else if (std::strncmp(input, "crash", 5) == 0) crash_root();
        else if (std::strncmp(input, "debugged", 8) == 0) std::puts(IsDebuggerPresent() ? "yes" : "no");
        else break;
        std::fflush(stdout);
    }
    return 0;
}
