package types

import "strings"

// The C library functions declared by the headers every program includes.
// An export function can't take one of these names.
var cLibraryNames = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(cLibraryList) {
		m[n] = true
	}
	return m
}()

const cLibraryList = `
a64l abort abs access accessx_np acct acos acosf acosh acoshf acoshl
acosl add_profil alarm aligned aligned_alloc alloc_align alloc_size
alloca alphasort arc4random arc4random_addrandom arc4random_buf
arc4random_stir arc4random_uniform asctime asctime_r asin asinf asinh
asinhf asinhl asinl asprintf at_quick_exit atan atan2 atan2f atan2l
atanf atanh atanhf atanhl atanl atexit atexit_b atof atoi atol atoll
availability bcmp bcopy brk bsd_signal bsearch bsearch_b bzero calloc
cbrt cbrtf cbrtl ceil ceilf ceill cgetcap cgetclose cgetent cgetfirst
cgetmatch cgetnext cgetnum cgetset cgetstr cgetustr chdir chflags chmod
chmodx_np chown chroot clearerr clock clock_getres clock_gettime
clock_gettime_nsec_np clock_settime close closedir confstr copysign
copysignf copysignl cos cosf cosh coshf coshl cosl crypt ctermid
ctermid_r ctime ctime_r daemon devname devname_r difftime dirfd div
dprintf drand48 dup dup2 ecvt encrypt endusershell erand48 erf erfc
erfcf erfcl erff erfl exchangedata execl execle execlp execv execve
execvp execvP exit exp exp2 exp2f exp2l expf expl expm1 expm1f expm1l
fabs fabsf fabsl faccessat fchdir fchflags fchmod fchmodat fchmodx_np
fchown fchownat fclose fcvt fdclosedir fdim fdimf fdiml fdopen fdopendir
fdscandir fdscandir_b feof ferror fflagstostr fflush ffs ffsctl ffsl
ffsll fgetattrlist fgetc fgetln fgetpos fgets fileno flockfile floor
floorf floorl fls flsl flsll fma fmaf fmal fmax fmaxf fmaxl fmemopen
fmin fminf fminl fmod fmodf fmodl fmtcheck fopen fork format_arg
fpathconf fpos_t fprintf fpurge fputc fputs fread freadlink free freopen
frexp frexpf frexpl fscanf fsctl fseek fseeko fsetattrlist fsetpos fstat
fstatat fstatx_np fsync fsync_volume_np ftell ftello ftruncate
ftrylockfile funlockfile funopen futimens fwrite gcvt getattrlist
getattrlistat getattrlistbulk getbsize getc getc_unlocked getchar
getchar_unlocked getcwd getdate getdelim getdirentries getdirentriesattr
getdomainname getdtablesize getegid getenv geteuid getgid getgrouplist
getgroups gethostid gethostname gethostuuid getiopolicy_np getline
getloadavg getlogin getlogin_r getmode getopt getpagesize getpass
getpeereid getpgid getpgrp getpid getppid getpriority getprogname
getrlimit getrusage gets getsgroups_np getsid getsubopt getuid
getusershell getw getwd getwgroups_np gmtime gmtime_r grantpt heapsort
heapsort_b hypot hypotf hypotl ilogb ilogbf ilogbl imaxabs imaxdiv index
initgroups initstate iruserok iruserok_sa isatty issetugid j0 j1 jn
jrand48 kdebug_signpost kill killpg l64a labs lchflags lchmod lchown
lcong48 ldexp ldexpf ldexpl ldiv lgamma lgammaf lgammal link linkat
llabs lldiv llrint llrintf llrintl llround llroundf llroundl localtime
localtime_r lockf log log10 log10f log10l log1p log1pf log1pl log2 log2f
log2l logb logbf logbl logf logl longjmp longjmperror lrand48 lrint
lrintf lrintl lround lroundf lroundl lseek lstat lstatx_np madvise main
malloc malloc_type_aligned_alloc malloc_type_calloc malloc_type_free
malloc_type_malloc malloc_type_posix_memalign malloc_type_realloc
malloc_type_valloc malloc_type_zone_calloc malloc_type_zone_free
malloc_type_zone_malloc malloc_type_zone_memalign
malloc_type_zone_realloc malloc_type_zone_valloc mblen mbstowcs mbtowc
memccpy memchr memcmp memcpy memmem memmove memset memset_pattern16
memset_pattern4 memset_pattern8 memset_s mergesort mergesort_b mincore
minherit mkdir mkdirat mkdirx_np mkdtemp mkdtempat_np mkfifo mkfifoat
mkfifox_np mknod mknodat mkostemp mkostemps mkostempsat_np mkpath_np
mkpathat_np mkstemp mkstemp_dprotected_np mkstemps mkstempsat_np mktemp
mktime mlock mlockall mmap modf modff modfl mprotect mrand48 msync
munlock munlockall munmap nan nanf nanl nanosleep nearbyint nearbyintf
nearbyintl nextafter nextafterf nextafterl nexttoward nexttowardf
nexttowardl nfssvc nice nonisolated nrand48 open_memstream opendir pack
pathconf pause pclose perror pipe popen posix_madvise posix_memalign
posix_openpt posix2time pow powf powl pread printf profil pselect
psignal psort psort_b psort_r pthread_atfork pthread_attr_destroy
pthread_attr_get_qos_class_np pthread_attr_getdetachstate
pthread_attr_getguardsize pthread_attr_getinheritsched
pthread_attr_getschedparam pthread_attr_getschedpolicy
pthread_attr_getscope pthread_attr_getstack pthread_attr_getstackaddr
pthread_attr_getstacksize pthread_attr_init
pthread_attr_set_qos_class_np pthread_attr_setdetachstate
pthread_attr_setguardsize pthread_attr_setinheritsched
pthread_attr_setschedparam pthread_attr_setschedpolicy
pthread_attr_setscope pthread_attr_setstack pthread_attr_setstackaddr
pthread_attr_setstacksize pthread_cancel pthread_cond_broadcast
pthread_cond_destroy pthread_cond_init pthread_cond_signal
pthread_cond_signal_thread_np pthread_cond_timedwait
pthread_cond_timedwait_relative_np pthread_cond_wait
pthread_condattr_destroy pthread_condattr_getpshared
pthread_condattr_init pthread_condattr_setpshared pthread_cpu_number_np
pthread_create pthread_create_suspended_np pthread_detach pthread_equal
pthread_exit pthread_from_mach_thread_np pthread_get_qos_class_np
pthread_get_stackaddr_np pthread_get_stacksize_np pthread_getconcurrency
pthread_getname_np pthread_getschedparam pthread_getspecific
pthread_getugid_np pthread_is_threaded_np
pthread_jit_write_freeze_callbacks_np pthread_jit_write_protect_np
pthread_jit_write_protect_supported_np
pthread_jit_write_with_callback_np pthread_join pthread_key_create
pthread_key_delete pthread_kill pthread_mach_thread_np pthread_main_np
pthread_mutex_destroy pthread_mutex_getprioceiling pthread_mutex_init
pthread_mutex_lock pthread_mutex_setprioceiling pthread_mutex_trylock
pthread_mutex_unlock pthread_mutexattr_destroy
pthread_mutexattr_getpolicy_np pthread_mutexattr_getprioceiling
pthread_mutexattr_getprotocol pthread_mutexattr_getpshared
pthread_mutexattr_gettype pthread_mutexattr_init
pthread_mutexattr_setpolicy_np pthread_mutexattr_setprioceiling
pthread_mutexattr_setprotocol pthread_mutexattr_setpshared
pthread_mutexattr_settype pthread_once pthread_override_qos_class_end_np
pthread_override_qos_class_start_np pthread_rwlock_destroy
pthread_rwlock_init pthread_rwlock_rdlock pthread_rwlock_tryrdlock
pthread_rwlock_trywrlock pthread_rwlock_unlock pthread_rwlock_wrlock
pthread_rwlockattr_destroy pthread_rwlockattr_getpshared
pthread_rwlockattr_init pthread_rwlockattr_setpshared pthread_self
pthread_set_qos_class_self_np pthread_setcancelstate
pthread_setcanceltype pthread_setconcurrency pthread_setname_np
pthread_setschedparam pthread_setspecific pthread_setugid_np
pthread_sigmask pthread_testcancel pthread_threadid_np pthread_yield_np
ptsname ptsname_r putc putc_unlocked putchar putchar_unlocked putenv
puts putw pwrite qos_class_main qos_class_self qsort qsort_b qsort_r
quick_exit radixsort raise rand rand_r random rcmd rcmd_af read readdir
readdir_r readlink readlinkat realloc reallocf realpath reboot remainder
remainderf remainderl remove remquo remquof remquol rename renameat
renameatx_np renamex_np revoke rewind rewinddir rindex rint rintf rintl
rmdir round roundf roundl rpmatch rresvport rresvport_af ruserok sbrk
scalb scalbln scalblnf scalblnl scalbn scalbnf scalbnl scandir scandir_b
scandirat scandirat_b scanf sched_get_priority_max
sched_get_priority_min sched_yield searchfs seed48 seekdir select
setattrlist setattrlistat setbuf setbuffer setdomainname setegid setenv
seteuid setgid setgroups sethostid sethostname setiopolicy_np setjmp
setkey setlinebuf setlogin setmode setpgid setpgrp setpriority
setprogname setregid setreuid setrgid setrlimit setruid setsgroups_np
setsid setstate setuid setusershell setvbuf setwgroups_np shm_open
shm_unlink sigaction sigaddset sigaltstack sigblock sigdelset
sigemptyset sigfillset sighold sigignore siginterrupt sigismember
siglongjmp signal sigpause sigpending sigprocmask sigrelse sigset
sigsetjmp sigsetmask sigsuspend sigvec sigwait sin sinf sinh sinhf sinhl
sinl sleep snprintf sprintf sqrt sqrtf sqrtl sradixsort srand srand48
sranddev srandom srandomdev sscanf stat statx_np stpcpy stpncpy
strcasecmp strcasestr strcat strchr strchrnul strcmp strcoll strcpy
strcspn strdup strerror strerror_r strftime strlcat strlcpy strlen
strmode strncasecmp strncat strncmp strncpy strndup strnlen strnstr
strpbrk strptime strrchr strsep strsignal strsignal_r strspn strstr
strtod strtof strtofflags strtoimax strtok strtok_r strtol strtold
strtoll strtonum strtoq strtoul strtoull strtoumax strtouq strxfrm swab
swapon symlink symlinkat sync sync_volume_np syscall sysconf system tan
tanf tanh tanhf tanhl tanl tcgetpgrp tcsetpgrp telldir tempnam tgamma
tgammaf tgammal time time2posix timegm timelocal timespec_get
timingsafe_bcmp tmpfile tmpnam trunc truncate truncf truncl ttyname
ttyname_r ttyslot tzset tzsetwall ualarm umask umaskx_np undelete ungetc
unlink unlinkat unlockpt unsetenv unwhiteout usleep utimensat valloc
vasprintf vdprintf vfork vfprintf vfscanf vprintf vscanf vsnprintf
vsprintf vsscanf wait wait3 wait4 waitid waitpid wcstoimax wcstombs
wcstoumax wctomb write y0 y1 yield yn
`
