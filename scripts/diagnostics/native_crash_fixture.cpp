// Disposable child only: never pointed at a real game/player.
#include <windows.h>
#include <cstdio>
#include <cstring>
#include <cstddef>
static void* protected_page;
static volatile int sink;
#pragma section(".crash", read, write, execute)
__declspec(allocate(".crash")) __declspec(align(4096)) unsigned char code_pages[8192] = {0x90};
__declspec(noinline) void read_leaf() { sink = *reinterpret_cast<volatile int*>(0); }
__declspec(noinline) void write_leaf() { *reinterpret_cast<volatile int*>(protected_page) = 42; }
__declspec(noinline) void execute_leaf() { reinterpret_cast<void(*)()>(protected_page)(); }
__declspec(noinline) void crash_leaf() { *reinterpret_cast<volatile int*>(0) = 42; }
__declspec(noinline) void crash_middle() { crash_leaf(); }
__declspec(noinline) void crash_root() { crash_middle(); }
int evidence(EXCEPTION_POINTERS* e) {
    const auto r = e->ExceptionRecord; const auto c = e->ContextRecord;
    std::printf("{\"address\":\"0x%0*llX\",\"fault\":\"0x%0*llX\",\"access\":%llu,\"registers\":{", int(sizeof(void*) * 2), (unsigned long long)r->ExceptionAddress,
        int(sizeof(void*) * 2), (unsigned long long)r->ExceptionInformation[1], (unsigned long long)r->ExceptionInformation[0]);
#define REG(name, member) std::printf("\"" name "\":\"0x%0*llX\",", int(sizeof(void*) * 2), (unsigned long long)c->member)
#ifdef _WIN64
    REG("RIP", Rip); REG("RSP", Rsp); REG("RBP", Rbp); REG("RAX", Rax); REG("RBX", Rbx); REG("RCX", Rcx); REG("RDX", Rdx); REG("RSI", Rsi); REG("RDI", Rdi);
    REG("R8", R8); REG("R9", R9); REG("R10", R10); REG("R11", R11); REG("R12", R12); REG("R13", R13); REG("R14", R14); REG("R15", R15);
    std::printf("\"RFlags\":\"0x%016llX\"", (unsigned long long)c->EFlags);
#else
    REG("EIP", Eip); REG("ESP", Esp); REG("EBP", Ebp); REG("EAX", Eax); REG("EBX", Ebx); REG("ECX", Ecx); REG("EDX", Edx); REG("ESI", Esi); REG("EDI", Edi);
    std::printf("\"EFlags\":\"0x%08lX\"", c->EFlags);
#endif
    std::puts("}}"); return EXCEPTION_EXECUTE_HANDLER;
}
int main(int argc, char** argv) {
    if (argc == 2 && std::strcmp(argv[1], "--layout") == 0) {
        std::printf("{\"debug_event\":%zu,\"union\":%zu,\"record\":%zu,\"code\":%zu,\"flags\":%zu,\"chain\":%zu,\"address\":%zu,\"count\":%zu,\"parameters\":%zu,\"first_chance\":%zu}\n",
            sizeof(DEBUG_EVENT), offsetof(DEBUG_EVENT, u), sizeof(EXCEPTION_RECORD), offsetof(EXCEPTION_RECORD, ExceptionCode), offsetof(EXCEPTION_RECORD, ExceptionFlags),
            offsetof(EXCEPTION_RECORD, ExceptionRecord), offsetof(EXCEPTION_RECORD, ExceptionAddress), offsetof(EXCEPTION_RECORD, NumberParameters), offsetof(EXCEPTION_RECORD, ExceptionInformation),
            offsetof(DEBUG_EVENT, u) + offsetof(EXCEPTION_DEBUG_INFO, dwFirstChance));
        return 0;
    }
    SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX);
    protected_page = VirtualAlloc(nullptr, 4096, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
    *reinterpret_cast<unsigned char*>(protected_page) = 0xc3;
    std::puts("ready"); std::fflush(stdout);
    char input[40];
    while (std::fgets(input, sizeof(input), stdin)) {
        if (std::strncmp(input, "code-", 5) == 0) {
            DWORD old, ignored;
            VirtualProtect(code_pages, 8192, PAGE_EXECUTE_READWRITE, &old);
            auto code = code_pages + 4080;
            // A null read at offset 4082, with only 14 readable image bytes left.
            code[0] = 0x31; code[1] = 0xc0; code[2] = 0x8b; code[3] = 0x00; code[4] = 0xc3;
            VirtualProtect(code_pages, 4096, input[5] == 'p' ? PAGE_EXECUTE_READ : PAGE_EXECUTE, &old);
            VirtualProtect(code_pages + 4096, 4096, PAGE_NOACCESS, &ignored);
            __try { reinterpret_cast<void(*)()>(code)(); } __except(evidence(GetExceptionInformation())) { std::puts("handled"); }
            VirtualProtect(code_pages, 8192, PAGE_EXECUTE_READWRITE, &ignored);
        } else if (std::strncmp(input, "fatal-", 6) == 0) {
            DWORD old; VirtualProtect(protected_page, 4096, input[6] == 'w' ? PAGE_READONLY : PAGE_READWRITE, &old);
            std::printf("0x%0*llX\n", int(sizeof(void*) * 2), input[6] == 'r' ? 0ull : (unsigned long long)protected_page); std::fflush(stdout);
            if (input[6] == 'r') read_leaf(); else if (input[6] == 'w') write_leaf(); else execute_leaf();
        } else if (std::strncmp(input, "case-", 5) == 0) {
            DWORD old; VirtualProtect(protected_page, 4096, input[5] == 'w' ? PAGE_READONLY : PAGE_READWRITE, &old);
            __try { if (input[5] == 'r') read_leaf(); else if (input[5] == 'w') write_leaf(); else execute_leaf(); }
            __except(evidence(GetExceptionInformation())) { std::puts("handled"); }
        } else if (std::strncmp(input, "module-cycle", 12) == 0) {
            auto library = LoadLibraryW(L"winmm.dll"); if (library) FreeLibrary(library); std::puts(library ? "cycled" : "failed");
        } else if (std::strncmp(input, "handled", 7) == 0) {
            __try { crash_root(); } __except(EXCEPTION_EXECUTE_HANDLER) { std::puts("handled"); }
        } else if (std::strncmp(input, "crash", 5) == 0) crash_root();
        else if (std::strncmp(input, "debugged", 8) == 0) std::puts(IsDebuggerPresent() ? "yes" : "no");
        else break;
        std::fflush(stdout);
    }
    return 0;
}
