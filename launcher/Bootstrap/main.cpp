#include <windows.h>
#include <string>

static bool run(const std::wstring& executable, const std::wstring& args,
                const std::wstring& directory, bool wait) {
  std::wstring command = L"\"" + executable + L"\" " + args;
  STARTUPINFOW startup = {sizeof(startup)};
  PROCESS_INFORMATION child = {};
  if (!CreateProcessW(executable.c_str(), command.data(), nullptr, nullptr, FALSE,
                      wait ? CREATE_NO_WINDOW : 0, nullptr, directory.c_str(), &startup, &child)) return false;
  DWORD code = 0;
  if (wait) { WaitForSingleObject(child.hProcess, INFINITE); GetExitCodeProcess(child.hProcess, &code); }
  CloseHandle(child.hThread); CloseHandle(child.hProcess);
  return code == 0;
}
int APIENTRY wWinMain(HINSTANCE, HINSTANCE, wchar_t*, int) {
  const bool korean = PRIMARYLANGID(GetUserDefaultUILanguage()) == LANG_KOREAN;
  wchar_t buffer[32768] = {};
  if (!GetModuleFileNameW(nullptr, buffer, 32768)) return 1;
  std::wstring root(buffer); root.resize(root.find_last_of(L"\\/"));
  const auto lock = root + L"\\.launcher-install.lock";
  HANDLE gate = CreateFileW(lock.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
  if (gate == INVALID_HANDLE_VALUE) { MessageBoxW(nullptr, korean ? L"런처를 업데이트하고 있습니다. 잠시 후 다시 실행하세요." : L"登录器正在更新，请稍后再打开。", L"DNF 90CN", MB_OK); return 2; }
  bool recovered = true;
  if (GetFileAttributesW((root + L"\\.launcher-update.json").c_str()) != INVALID_FILE_ATTRIBUTES)
    recovered = run(root + L"\\update-helper.exe", L"--recover-bundle \"" + root + L"\"", root, true);
  CloseHandle(gate);
  if (!recovered || !run(root + L"\\app\\DNF90.exe", L"", root + L"\\app", false)) {
    MessageBoxW(nullptr, korean ? L"런처 파일이 없거나 복구에 실패했습니다. app 폴더를 유지하고 전체 패키지를 다시 압축 해제하세요." : L"登录器文件不完整或恢复失败。请保持 app 文件夹完整，并重新解压完整发布包。", L"DNF 90CN", MB_OK | MB_ICONERROR); return 3;
  }
  return 0;
}
