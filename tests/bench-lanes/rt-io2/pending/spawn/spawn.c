/* spawn, C: n runs of this program with "child" (which exits 0 at once),
 * every stream the null device, each waited for: CreateProcessW and
 * WaitForSingleObject on Windows, posix_spawn and waitpid on Linux.
 * Prints n, the sum of the exit codes and the failed spawns.
 * Usage: spawn <n>. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
int wmain(int argc, wchar_t **argv) {
    if (wcscmp(argv[1], L"child") == 0) return 0;
    long n = wcstol(argv[1], NULL, 10);
    wchar_t self[32768];
    GetModuleFileNameW(NULL, self, 32768);
    wchar_t cmd[33000];
    _snwprintf(cmd, 33000, L"\"%s\" child", self);
    SECURITY_ATTRIBUTES sa = {sizeof sa, NULL, TRUE};
    HANDLE nul = CreateFileW(L"NUL", GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, NULL);
    long long sum = 0;
    long fails = 0;
    for (long i = 0; i < n; i++) {
        STARTUPINFOW si;
        memset(&si, 0, sizeof si);
        si.cb = sizeof si;
        si.dwFlags = STARTF_USESTDHANDLES;
        si.hStdInput = si.hStdOutput = si.hStdError = nul;
        PROCESS_INFORMATION pi;
        wchar_t line[33000];
        wcscpy(line, cmd);
        if (!CreateProcessW(self, line, NULL, NULL, TRUE, 0, NULL, NULL, &si, &pi)) {
            fails++;
            continue;
        }
        CloseHandle(pi.hThread);
        WaitForSingleObject(pi.hProcess, INFINITE);
        DWORD code = 0;
        GetExitCodeProcess(pi.hProcess, &code);
        sum += code;
        CloseHandle(pi.hProcess);
    }
    printf("%ld %lld %ld\n", n, sum, fails);
    return 0;
}
#else
#include <spawn.h>
#include <sys/wait.h>
#include <fcntl.h>
#include <unistd.h>
extern char **environ;
int main(int argc, char **argv) {
    if (strcmp(argv[1], "child") == 0) return 0;
    long n = strtol(argv[1], NULL, 10);
    char self[4096];
    ssize_t k = readlink("/proc/self/exe", self, sizeof self - 1);
    self[k < 0 ? 0 : k] = 0;
    posix_spawn_file_actions_t fa;
    posix_spawn_file_actions_init(&fa);
    for (int fd = 0; fd < 3; fd++) posix_spawn_file_actions_addopen(&fa, fd, "/dev/null", O_RDWR, 0);
    char *cargv[] = {self, "child", NULL};
    long long sum = 0;
    long fails = 0;
    for (long i = 0; i < n; i++) {
        pid_t pid;
        if (posix_spawn(&pid, self, &fa, NULL, cargv, environ) != 0) {
            fails++;
            continue;
        }
        int st = 0;
        while (waitpid(pid, &st, 0) < 0) {
        }
        sum += WIFEXITED(st) ? WEXITSTATUS(st) : -WTERMSIG(st);
    }
    printf("%ld %lld %ld\n", n, sum, fails);
    return 0;
}
#endif
