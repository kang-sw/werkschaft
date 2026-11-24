#include "textflag.h"

TEXT acl_get_fd_trampoline<>(SB), NOSPLIT, $0-0
	JMP libc_acl_get_fd(SB)
GLOBL ·aclGetFD(SB), RODATA, $8
DATA ·aclGetFD(SB)/8, $acl_get_fd_trampoline<>(SB)

TEXT acl_set_fd_trampoline<>(SB), NOSPLIT, $0-0
	JMP libc_acl_set_fd(SB)
GLOBL ·aclSetFD(SB), RODATA, $8
DATA ·aclSetFD(SB)/8, $acl_set_fd_trampoline<>(SB)

TEXT acl_free_trampoline<>(SB), NOSPLIT, $0-0
	JMP libc_acl_free(SB)
GLOBL ·aclFree(SB), RODATA, $8
DATA ·aclFree(SB)/8, $acl_free_trampoline<>(SB)

TEXT acl_size_trampoline<>(SB), NOSPLIT, $0-0
	JMP libc_acl_size(SB)
GLOBL ·aclSize(SB), RODATA, $8
DATA ·aclSize(SB)/8, $acl_size_trampoline<>(SB)

TEXT acl_copy_ext_trampoline<>(SB), NOSPLIT, $0-0
	JMP libc_acl_copy_ext(SB)
GLOBL ·aclCopyExt(SB), RODATA, $8
DATA ·aclCopyExt(SB)/8, $acl_copy_ext_trampoline<>(SB)

TEXT acl_copy_int_trampoline<>(SB), NOSPLIT, $0-0
	JMP libc_acl_copy_int(SB)
GLOBL ·aclCopyInt(SB), RODATA, $8
DATA ·aclCopyInt(SB)/8, $acl_copy_int_trampoline<>(SB)
