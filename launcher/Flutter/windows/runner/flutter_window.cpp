#include "flutter_window.h"

#include <optional>
#include <algorithm>
#include <flutter/standard_method_codec.h>
#include <dwmapi.h>
#include <shobjidl.h>
#include <wrl/client.h>
#include <windowsx.h>

#include "flutter/generated_plugin_registrant.h"
#include "resource.h"
#include "utils.h"

namespace {
constexpr UINT kTrayMessage = WM_APP + 1;
constexpr UINT kTrayOpen = 1;
constexpr UINT kTrayExit = 2;

HRESULT SaveDiagnosticLog(HWND owner, const flutter::EncodableValue* arguments,
                          std::string* selected) {
  Microsoft::WRL::ComPtr<IFileSaveDialog> dialog;
  HRESULT hr = CoCreateInstance(CLSID_FileSaveDialog, nullptr,
      CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&dialog));
  if (FAILED(hr)) return hr;
  DWORD options = 0;
  if (FAILED(hr = dialog->GetOptions(&options))) return hr;
  if (FAILED(hr = dialog->SetOptions(options | FOS_FORCEFILESYSTEM |
      FOS_PATHMUSTEXIST | FOS_OVERWRITEPROMPT | FOS_NOCHANGEDIR | FOS_STRICTFILETYPES))) return hr;
  const COMDLG_FILTERSPEC types[] = {{L"诊断日志 (*.log)", L"*.log"}};
  dialog->SetFileTypes(1, types);
  dialog->SetDefaultExtension(L"log");
  dialog->SetTitle(L"PurpleMagic — 日志另存为");
  dialog->SetFileName(L"PurpleMagic.log");
  const auto* fields = arguments ? std::get_if<flutter::EncodableMap>(arguments) : nullptr;
  if (fields) {
    const auto field = fields->find(flutter::EncodableValue("fileName"));
    const auto* name = field != fields->end() ? std::get_if<std::string>(&field->second) : nullptr;
    if (name && name->size() <= 200 && name->find_first_of("\\/:") == std::string::npos) {
      const int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, name->c_str(), -1, nullptr, 0);
      if (length > 0) {
        std::wstring wide(length, L'\0');
        MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, name->c_str(), -1, wide.data(), length);
        dialog->SetFileName(wide.c_str());
      }
    }
  }
  if (FAILED(hr = dialog->Show(owner))) return hr;
  Microsoft::WRL::ComPtr<IShellItem> file;
  if (FAILED(hr = dialog->GetResult(&file))) return hr;
  PWSTR path = nullptr;
  hr = file->GetDisplayName(SIGDN_FILESYSPATH, &path);
  if (SUCCEEDED(hr)) *selected = Utf8FromUtf16(path);
  CoTaskMemFree(path);
  return hr;
}

HRESULT ChooseDirectory(HWND owner, const flutter::EncodableValue* arguments,
                        std::string* selected) {
  Microsoft::WRL::ComPtr<IFileOpenDialog> dialog;
  HRESULT hr = CoCreateInstance(CLSID_FileOpenDialog, nullptr,
      CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&dialog));
  if (FAILED(hr)) return hr;
  DWORD options = 0;
  hr = dialog->GetOptions(&options);
  if (FAILED(hr)) return hr;
  hr = dialog->SetOptions(options | FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM |
                         FOS_PATHMUSTEXIST | FOS_NOCHANGEDIR);
  if (FAILED(hr)) return hr;
  dialog->SetTitle(L"PurpleMagic — 选择包含 DNF.exe 和 Script.pvf 的客户端目录");
  dialog->SetOkButtonLabel(L"选择此目录");
  const auto* fields = arguments ? std::get_if<flutter::EncodableMap>(arguments) : nullptr;
  if (fields) {
    const auto field = fields->find(flutter::EncodableValue("initialDirectory"));
    const auto* initial = field != fields->end() ? std::get_if<std::string>(&field->second) : nullptr;
    if (initial && !initial->empty()) {
      const int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS,
          initial->c_str(), -1, nullptr, 0);
      if (length > 0) {
        std::wstring path(length, L'\0');
        MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, initial->c_str(), -1,
                            path.data(), length);
        Microsoft::WRL::ComPtr<IShellItem> folder;
        if (SUCCEEDED(SHCreateItemFromParsingName(path.c_str(), nullptr,
                                                 IID_PPV_ARGS(&folder)))) {
          dialog->SetFolder(folder.Get());
        }
      }
    }
  }
  hr = dialog->Show(owner);
  if (FAILED(hr)) return hr;
  Microsoft::WRL::ComPtr<IShellItem> folder;
  hr = dialog->GetResult(&folder);
  if (FAILED(hr)) return hr;
  PWSTR path = nullptr;
  hr = folder->GetDisplayName(SIGDN_FILESYSPATH, &path);
  if (SUCCEEDED(hr)) *selected = Utf8FromUtf16(path);
  CoTaskMemFree(path);
  return hr;
}
}  // namespace

FlutterWindow::FlutterWindow(const flutter::DartProject& project)
    : project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) {
    return false;
  }

  RECT frame = GetClientArea();

  // The size here must match the window dimensions to avoid unnecessary surface
  // creation / destruction in the startup path.
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  // Ensure that basic setup of the controller was successful.
  if (!flutter_controller_->engine() || !flutter_controller_->view()) {
    return false;
  }
  RegisterPlugins(flutter_controller_->engine());
  SetChildContent(flutter_controller_->view()->GetNativeWindow());

  tray_icon_.cbSize = sizeof(tray_icon_);
  tray_icon_.hWnd = GetHandle();
  tray_icon_.uID = 1;
  tray_icon_.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP | NIF_SHOWTIP;
  tray_icon_.uCallbackMessage = kTrayMessage;
  tray_icon_.hIcon = LoadIcon(GetModuleHandle(nullptr), MAKEINTRESOURCE(IDI_APP_ICON));
  wcscpy_s(tray_icon_.szTip, L"PurpleMagic — 点击恢复登录器");
  taskbar_created_message_ = RegisterWindowMessage(L"TaskbarCreated");
  if (taskbar_created_message_) {
    // Explorer also needs to notify a launcher started as administrator.
    ChangeWindowMessageFilterEx(GetHandle(), taskbar_created_message_, MSGFLT_ALLOW, nullptr);
  }

  window_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "dnf90.preview/window",
      &flutter::StandardMethodCodec::GetInstance());
  window_channel_->SetMethodCallHandler(
      [this](const flutter::MethodCall<flutter::EncodableValue>& call,
             std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result) {
        const auto window = GetHandle();
        const auto& name = call.method_name();
        if (name == "chooseDirectory" || name == "saveDiagnosticLog") {
          std::string selected;
          const HRESULT hr = name == "saveDiagnosticLog"
              ? SaveDiagnosticLog(window, call.arguments(), &selected)
              : ChooseDirectory(window, call.arguments(), &selected);
          if (hr == HRESULT_FROM_WIN32(ERROR_CANCELLED)) {
            result->Success();
          } else if (FAILED(hr) || selected.empty()) {
            result->Error("picker_failed", "无法读取所选位置，请重试。");
          } else {
            result->Success(flutter::EncodableValue(selected));
          }
          return;
        } else if (name == "minimize") {
          ShowWindow(window, SW_MINIMIZE);
        } else if (name == "maximize") {
          ShowWindow(window, IsZoomed(window) ? SW_RESTORE : SW_MAXIMIZE);
        } else if (name == "drag") {
          ReleaseCapture();
          PostMessage(window, WM_NCLBUTTONDOWN, HTCAPTION, 0);
        } else if (name == "close") {
          PostMessage(window, WM_CLOSE, 0, 0);
        } else {
          result->NotImplemented();
          return;
        }
        result->Success();
      });
  // Windows 11 rounded corners; older Windows safely ignores this attribute.
  const DWORD rounded = 2;
  DwmSetWindowAttribute(GetHandle(), 33, &rounded, sizeof(rounded));

  flutter_controller_->engine()->SetNextFrameCallback([&]() {
    this->Show();
  });

  // Flutter can complete the first frame before the "show window" callback is
  // registered. The following call ensures a frame is pending to ensure the
  // window is shown. It is a no-op if the first frame hasn't completed yet.
  flutter_controller_->ForceRedraw();

  return true;
}

void FlutterWindow::OnDestroy() {
  RemoveTrayIcon();
  window_channel_ = nullptr;
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

bool FlutterWindow::AddTrayIcon() {
  if (tray_icon_visible_) return true;
  // A replacement shell may broadcast TaskbarCreated with the icon still
  // registered. Refresh that entry instead of treating it as an unavailable tray.
  if (!Shell_NotifyIconW(NIM_ADD, &tray_icon_) &&
      !Shell_NotifyIconW(NIM_MODIFY, &tray_icon_)) return false;
  tray_icon_visible_ = true;
  tray_icon_.uVersion = NOTIFYICON_VERSION_4;
  tray_version_4_ = Shell_NotifyIconW(NIM_SETVERSION, &tray_icon_) != FALSE;
  return true;
}

void FlutterWindow::RemoveTrayIcon() {
  if (!tray_icon_visible_) return;
  Shell_NotifyIconW(NIM_DELETE, &tray_icon_);
  tray_icon_visible_ = false;
}

void FlutterWindow::RestoreFromTray() {
  ShowWindow(GetHandle(), SW_RESTORE);
  SetForegroundWindow(GetHandle());
  RemoveTrayIcon();
}

void FlutterWindow::ShowTrayMenu(POINT position) {
  HMENU menu = CreatePopupMenu();
  if (!menu) return;
  AppendMenuW(menu, MF_STRING, kTrayOpen, L"打开登录器");
  AppendMenuW(menu, MF_STRING, kTrayExit, L"退出登录器");
  SetMenuDefaultItem(menu, kTrayOpen, FALSE);
  if (position.x == -1 && position.y == -1) {
    NOTIFYICONIDENTIFIER identifier{};
    identifier.cbSize = sizeof(identifier);
    identifier.hWnd = GetHandle();
    identifier.uID = tray_icon_.uID;
    RECT bounds{};
    if (SUCCEEDED(Shell_NotifyIconGetRect(&identifier, &bounds))) {
      position = {bounds.left, bounds.top};
    } else {
      GetCursorPos(&position);
    }
  }
  // Set foreground before tracking and post WM_NULL after it so clicking away
  // dismisses the menu even while the owner window is hidden.
  SetForegroundWindow(GetHandle());
  const UINT command = TrackPopupMenuEx(menu,
      TPM_RETURNCMD | TPM_NONOTIFY | TPM_RIGHTBUTTON,
      position.x, position.y, GetHandle(), nullptr);
  DestroyMenu(menu);
  PostMessage(GetHandle(), WM_NULL, 0, 0);
  if (command == kTrayOpen) RestoreFromTray();
  if (command == kTrayExit) PostMessage(GetHandle(), WM_CLOSE, 0, 0);
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
  if (taskbar_created_message_ && message == taskbar_created_message_) {
    if (tray_icon_visible_) {
      tray_icon_visible_ = false;
      if (!AddTrayIcon()) RestoreFromTray();
    }
    return 0;
  }
  if (message == kTrayMessage) {
    const UINT event = tray_version_4_ ? LOWORD(lparam) : static_cast<UINT>(lparam);
    if (event == NIN_SELECT || event == NIN_KEYSELECT ||
        (!tray_version_4_ && event == WM_LBUTTONUP)) {
      RestoreFromTray();
    } else if (event == WM_CONTEXTMENU || (!tray_version_4_ && event == WM_RBUTTONUP)) {
      POINT position{GET_X_LPARAM(wparam), GET_Y_LPARAM(wparam)};
      if (!tray_version_4_) GetCursorPos(&position);
      ShowTrayMenu(position);
    }
    return 0;
  }
  if (message == WM_SIZE && wparam == SIZE_MINIMIZED && AddTrayIcon()) {
    // Hide only after the tray entry exists. No WM_CLOSE/engine disposal: the
    // core pipe, downloads and game protection remain alive while minimized.
    ShowWindow(hwnd, SW_HIDE);
    return 0;
  }
  if (message == WM_WINDOWPOSCHANGED && IsWindowVisible(hwnd) && !IsIconic(hwnd)) {
    // Also covers restoring the existing window by starting a second launcher.
    RemoveTrayIcon();
  }

  // Give Flutter, including plugins, an opportunity to handle window messages.
  if (flutter_controller_) {
    std::optional<LRESULT> result =
        flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam,
                                                      lparam);
    if (result) {
      return *result;
    }
  }

  switch (message) {
    case WM_NCCALCSIZE:
      if (wparam) {
        if (IsZoomed(hwnd)) {
          auto* params = reinterpret_cast<NCCALCSIZE_PARAMS*>(lparam);
          MONITORINFO monitor = {sizeof(MONITORINFO)};
          GetMonitorInfo(MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST), &monitor);
          params->rgrc[0] = monitor.rcWork;
        }
        return 0;
      }
      break;
    case WM_GETMINMAXINFO: {
      auto* size = reinterpret_cast<MINMAXINFO*>(lparam);
      const UINT dpi = GetDpiForWindow(hwnd);
      size->ptMinTrackSize.x = MulDiv(1000, dpi, 96);
      size->ptMinTrackSize.y = MulDiv(620, dpi, 96);
      MONITORINFO monitor = {sizeof(MONITORINFO)};
      if (GetMonitorInfo(MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST), &monitor)) {
        size->ptMinTrackSize.x = std::min(size->ptMinTrackSize.x,
            monitor.rcWork.right - monitor.rcWork.left);
        size->ptMinTrackSize.y = std::min(size->ptMinTrackSize.y,
            monitor.rcWork.bottom - monitor.rcWork.top);
      }
      return 0;
    }
    case WM_FONTCHANGE:
      flutter_controller_->engine()->ReloadSystemFonts();
      break;
  }

  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
