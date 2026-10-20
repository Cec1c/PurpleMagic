#include "flutter_window.h"

#include <optional>
#include <algorithm>
#include <flutter/standard_method_codec.h>
#include <dwmapi.h>
#include <shobjidl.h>
#include <wrl/client.h>

#include "flutter/generated_plugin_registrant.h"
#include "utils.h"

namespace {
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
    const auto purpose = fields->find(flutter::EncodableValue("purpose"));
    const auto* value = purpose != fields->end() ? std::get_if<std::string>(&purpose->second) : nullptr;
    if (value && *value == "diagnosticExport") dialog->SetTitle(L"PurpleMagic — 选择诊断包导出目录");
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

  window_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "dnf90.preview/window",
      &flutter::StandardMethodCodec::GetInstance());
  window_channel_->SetMethodCallHandler(
      [this](const flutter::MethodCall<flutter::EncodableValue>& call,
             std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result) {
        const auto window = GetHandle();
        const auto& name = call.method_name();
        if (name == "chooseDirectory") {
          std::string selected;
          const HRESULT hr = ChooseDirectory(window, call.arguments(), &selected);
          if (hr == HRESULT_FROM_WIN32(ERROR_CANCELLED)) {
            result->Success();
          } else if (FAILED(hr) || selected.empty()) {
            result->Error("directory_picker_failed", "无法读取所选目录，请重试。");
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
  window_channel_ = nullptr;
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
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
