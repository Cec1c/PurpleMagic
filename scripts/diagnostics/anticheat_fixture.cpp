#include <windows.h>
// Isolated test process; never connects to a server or reads player assets.
int WINAPI WinMain(HINSTANCE, HINSTANCE, LPSTR, int) { Sleep(60000); return 0; }
