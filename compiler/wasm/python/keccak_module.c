// Fixed CPython/WASI bridge. Cryptography is implemented by the Go host.
#define PY_SSIZE_T_CLEAN
#include "Python.h"
#include <stdint.h>

__attribute__((import_module("etherview"), import_name("keccak256")))
extern uint32_t etherview_keccak256(const uint8_t *, uint32_t, uint8_t *);

static PyObject *keccak256(PyObject *self, PyObject *argument) {
    (void)self;
    Py_buffer input;
    if (PyObject_GetBuffer(argument, &input, PyBUF_SIMPLE) < 0) return NULL;
    uint8_t output[32];
    uint32_t status = 1;
    if (input.len >= 0 && (uint64_t)input.len <= UINT32_MAX)
        status = etherview_keccak256(input.buf, (uint32_t)input.len, output);
    PyBuffer_Release(&input);
    if (status != 0) {
        PyErr_SetString(PyExc_ValueError, "compiler hash input rejected");
        return NULL;
    }
    return PyBytes_FromStringAndSize((const char *)output, sizeof(output));
}
static PyMethodDef methods[] = {
    {"keccak256", keccak256, METH_O, "Compute Keccak-256 using the fixed host ABI."},
    {NULL, NULL, 0, NULL}
};
static struct PyModuleDef module = {PyModuleDef_HEAD_INIT, "_etherview_keccak", NULL, -1, methods};
PyMODINIT_FUNC PyInit__etherview_keccak(void) {return PyModule_Create(&module);}
